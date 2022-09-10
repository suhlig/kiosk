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
	"uhlig.it/kiosk/kiosk"
	"uhlig.it/kiosk/script"
)

var opts struct {
	Version      bool          `short:"V" long:"version" description:"Print version information and exit"`
	Verbose      bool          `short:"v" long:"verbose" description:"Print verbose information"`
	Kiosk        bool          `short:"k" long:"kiosk" description:"Run in kiosk mode"`
	Interval     time.Duration `short:"i" long:"interval" description:"how long to wait before switching to the next tab. Anything Go's time#ParseDuration understands is accepted." default:"5s"`
	MqttClientID string        `short:"c" long:"client-id" description:"client id to use for the MQTT connection"`
	MqttURL      string        `short:"m" long:"mqtt-url"  description:"URL of the MQTT broker incl. username and password" env:"MQTT_URL"`
	Args         struct {
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

	kiosk := kiosk.NewKiosk().
		WithInterval(opts.Interval).
		WithVerbose(opts.Verbose).
		WithFullScreen(opts.Kiosk)

	// first tab is special
	err = kiosk.FirstTab(tabs[0])

	if err != nil {
		log.Fatal(err)
	}

	// all other tabs are equal
	for _, tab := range tabs[1:] {
		err = kiosk.AdditionalTab(tab)

		if err != nil {
			log.Fatal(err)
		}
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
		log.Println("Server started at port 8080")
		log.Fatal(http.ListenAndServe(":8080", nil))
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

func configureMqtt(kiosk *kiosk.Kiosk) error {
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
			log.Printf("Reconnecting to MQTT at %s\n", mqttURL.String())
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

func createConnectHandler(kiosk *kiosk.Kiosk, mqttURL *url.URL) func(mqtt.Client) {
	topic := strings.TrimPrefix(mqttURL.Path, "/")

	return func(mqttClient mqtt.Client) {
		if opts.Verbose {
			log.Printf("Connected to MQTT at %v\n", mqttURL.Host)
		}

		if opts.Verbose {
			log.Printf("Subscribing to %v\n", topic)
		}

		token := mqttClient.Subscribe(topic, 0, func(c mqtt.Client, m mqtt.Message) {
			command := string(m.Payload())

			if opts.Verbose {
				log.Printf("Received MQTT command '%v'\n", command)
			}

			switch command {
			case "pause":
				kiosk.PauseTabSwitching()
			case "resume":
				kiosk.StartTabSwitching()
			case "next":
				kiosk.NextTab()
			case "previous":
				kiosk.PreviousTab()
			default:
				log.Printf("Could not interpret MQTT command '%v'\n", command)
			}
		})

		if !token.WaitTimeout(10 * time.Second) {
			log.Fatalf("Could not subscribe: %v", token.Error())
		}
	}
}

func createRootHandler(kiosk *kiosk.Kiosk) http.HandlerFunc {
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

func createImageHandler(kiosk *kiosk.Kiosk) http.HandlerFunc {
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

func createActivateHandler(kiosk *kiosk.Kiosk) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Only POST allowed here", http.StatusMethodNotAllowed)
			return
		}

		if err := r.ParseForm(); err != nil {
			log.Printf("Could not parse form parameters: %v", err)
			http.Error(w, "Could not parse form parameters", http.StatusUnprocessableEntity)

			return
		}

		log.Printf("TODO activate tab with ID %v", r.FormValue("id"))

		http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
	}
}
