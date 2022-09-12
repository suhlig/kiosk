package main

import (
	"embed"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/template"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/jessevdk/go-flags"
	"uhlig.it/kiosk/controller"
	"uhlig.it/kiosk/script"
)

var opts struct {
	Version         bool          `short:"V" long:"version" description:"Print version information and exit"`
	Verbose         bool          `short:"v" long:"verbose" description:"Print verbose information"`
	Kiosk           bool          `short:"k" long:"kiosk" description:"Run in kiosk mode"`
	Interval        time.Duration `short:"i" long:"interval" description:"how long to wait before switching to the next tab. Anything Go's time#ParseDuration understands is accepted." default:"5s"`
	MqttClientID    string        `short:"c" long:"client-id" description:"client id to use for the MQTT connection"`
	MqttURL         string        `short:"m" long:"mqtt-url" description:"URL of the MQTT broker incl. username and password" env:"MQTT_URL"`
	HttpBindAddress string        `short:"a" long:"http-address" description:"Address to bind the HTTP control server to" default:"localhost:8011"`
	ChromeFlags     []string      `long:"chrome-flag" description:"additional flags to pass to chromium"`
	Args            struct {
		Scriptfile string
	} `positional-args:"yes"`
}

// ldflags will be set by goreleaser
var version = "vDEV"
var commit = "NONE"
var date = "UNKNOWN"

//go:embed *.html.tmpl
var htmlAssets embed.FS

func main() {
	log.SetFlags(0) // no timestamp etc. - we have systemd's timestamps in the log anyway

	_, err := flags.Parse(&opts)

	if err != nil {
		log.Fatal(err)
	}

	if opts.Version {
		log.Printf("%s %s (%s), built on %s\n", getProgramName(), version, commit, date)
		os.Exit(0)
	}

	var scriptBytes []byte

	if opts.Args.Scriptfile == "" {
		scriptBytes, err = io.ReadAll(os.Stdin)
	} else {
		scriptBytes, err = os.ReadFile(opts.Args.Scriptfile)
	}

	if err != nil {
		log.Fatalf("Could not read scriptfile: %v\n", err)
	}

	tabs, err := script.Parse(scriptBytes)

	if err != nil {
		log.Fatalf("Could not parse scriptfile %v: %v\n", opts.Args.Scriptfile, err)
	}

	kiosk := controller.NewKiosk().
		WithInterval(opts.Interval).
		WithFullScreen(opts.Kiosk)

	for _, cf := range opts.ChromeFlags {
		key, value, found := strings.Cut(cf, "=")

		if !found {
			log.Fatalf("Could not separate chrome flag %v; expecting k=v\n", cf)
		}

		kiosk = kiosk.WithFlag(key, value)
	}

	for _, tab := range tabs {
		if opts.Verbose {
			log.Printf("MAIN Performing actions for tab %s:\n", tab)

			for _, a := range tab.Steps {
				log.Printf("       * %s\n", a)
			}
		}

		err = kiosk.NewTab(tab)

		if err != nil {
			log.Fatal(err)
		}
	}

	if opts.Verbose {
		log.Println("MAIN Starting tab switching")
	}

	kiosk.StartTabSwitching()

	err = configureMqtt(kiosk)

	if err != nil {
		log.Fatalf("Could not connect to MQTT: %s", err)
	}

	quitProgram := make(chan struct{})
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		kiosk.Close()
		close(quitProgram)
	}()

	http.Handle("/", createRootHandler(kiosk))
	http.Handle("/image/", createImageHandler(kiosk))
	http.Handle("/activate/", createActivateHandler(kiosk))

	go func() {
		log.Printf("HTTP control server starting at http://%v\n", opts.HttpBindAddress)
		log.Fatal(http.ListenAndServe(opts.HttpBindAddress, nil))
	}()

	<-quitProgram
}

func getProgramName() string {
	path, err := os.Executable()

	if err != nil {
		fmt.Fprintln(os.Stderr, "Warning: Could not determine program name; using 'unknown'.")
		return "unknown"
	}

	return filepath.Base(path)
}

func configureMqtt(kiosk *controller.Kiosk) error {
	mqttURL, err := url.Parse(opts.MqttURL)

	if err != nil {
		return err
	}

	mqttClientID := opts.MqttClientID

	if mqttClientID == "" {
		mqttClientID = getProgramName()
	}

	mqttOpts := mqtt.NewClientOptions().
		AddBroker(mqttURL.String()).
		SetClientID(mqttClientID).
		SetCleanSession(false).
		SetUsername(mqttURL.User.Username()).
		SetAutoReconnect(true)

	mqttOpts.OnConnect = createConnectHandler(kiosk, mqttURL)

	mqttOpts.OnReconnecting = func(client mqtt.Client, options *mqtt.ClientOptions) {
		if opts.Verbose {
			log.Printf("MQTT Reconnecting to %s\n", mqttURL.String())
		}
	}

	password, isSet := mqttURL.User.Password()

	if isSet {
		mqttOpts.SetPassword(password)
	}

	if opts.Verbose {
		mqtt.WARN = log.New(os.Stderr, "MQTT WARN ", 0)
	}

	mqtt.CRITICAL = log.New(os.Stderr, "MQTT CRITICAL ", 0)
	mqtt.ERROR = log.New(os.Stderr, "MQTT ERROR ", 0)

	mqttClient := mqtt.NewClient(mqttOpts)

	if token := mqttClient.Connect(); token.Wait() && token.Error() != nil {
		return token.Error()
	}

	return nil
}

func createConnectHandler(kiosk *controller.Kiosk, mqttURL *url.URL) func(mqtt.Client) {
	topic := strings.TrimPrefix(mqttURL.Path, "/")

	return func(mqttClient mqtt.Client) {
		if opts.Verbose {
			log.Printf("MQTT Connected to  %v\n", mqttURL.Host)
		}

		if opts.Verbose {
			log.Printf("MQTT Subscribing to %v\n", topic)
		}

		token := mqttClient.Subscribe(topic, 0, func(c mqtt.Client, m mqtt.Message) {
			command := string(m.Payload())

			if opts.Verbose {
				log.Printf("MQTT Received command '%v'\n", command)
			}

			switch command {
			case "pause":
				if opts.Verbose {
					log.Println("MQTT Stopping tab switching")
				}

				kiosk.PauseTabSwitching()
			case "resume":
				if opts.Verbose {
					log.Println("MQTT Resuming tab switching")
				}

				kiosk.StartTabSwitching()
			case "next":
				if opts.Verbose {
					log.Println("MQTT Switch to next tab")
				}

				kiosk.NextTab()
			case "previous":
				if opts.Verbose {
					log.Println("MQTT Switch to previous tab")
				}

				kiosk.PreviousTab()
			default:
				log.Printf("MQTT Could not interpret command '%v'\n", command)
			}
		})

		if !token.WaitTimeout(10 * time.Second) {
			log.Fatalf("MQTT Could not subscribe: %v", token.Error())
		}
	}
}

func createRootHandler(kiosk *controller.Kiosk) http.HandlerFunc {
	tmpl, err := template.ParseFS(htmlAssets, "index.html.tmpl")

	if err != nil {
		log.Fatal(err)
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "text/html")
		tmpl.Execute(w, kiosk.ImageIDs())
	}
}

func createImageHandler(kiosk *controller.Kiosk) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		imageID := strings.TrimPrefix(r.URL.Path, "/image/")

		img, found := kiosk.GetImage(imageID)

		if !found {
			http.NotFound(w, r)
			fmt.Fprintf(w, "No image for target ID %v", imageID)
			return
		}

		w.Header().Set("Content-Type", "image/png")
		w.Write(img.GetData())
	}
}

func createActivateHandler(kiosk *controller.Kiosk) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Only POST allowed here", http.StatusMethodNotAllowed)
			return
		}

		if err := r.ParseForm(); err != nil {
			log.Printf("HTTP Could not parse form parameters: %v", err)
			http.Error(w, "Could not parse form parameters", http.StatusUnprocessableEntity)
			return
		}

		targetID := r.FormValue("id")

		if opts.Verbose {
			log.Printf("HTTP Switching to tab %v", targetID)
		}

		err := kiosk.SwitchToTab(targetID)

		if err != nil {
			log.Printf("HTTP Could not switch to tab: %v", err)
			http.Error(w, "Could not switch to tab", http.StatusUnprocessableEntity)
			return
		}

		http.Redirect(w, r, fmt.Sprintf("/#%v", targetID), http.StatusTemporaryRedirect)
	}
}
