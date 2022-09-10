package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/jessevdk/go-flags"
	"uhlig.it/kiosk/script"
)

type Image struct {
	mutex sync.RWMutex
	data  []byte
}

func (d *Image) Store(data []byte) {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	d.data = data
}

func (d *Image) Get() []byte {
	d.mutex.RLock()
	defer d.mutex.RUnlock()

	return d.data
}

type Kiosk struct {
	CurrentTab  target.ID
	AllContexts []context.Context
	Images      map[target.ID]*Image // TODO implement finder
}

func NewKiosk() *Kiosk {
	return &Kiosk{
		Images: make(map[target.ID]*Image),
	}
}

func (t *Kiosk) AppendContext(ctx context.Context) {
	t.AllContexts = append(t.AllContexts, ctx)
}

func (t *Kiosk) RootContext() context.Context {
	return t.AllContexts[0]
}

func (t *Kiosk) SetCurrentTab(id target.ID) {
	(*t).CurrentTab = id
}

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

	allocatorOptions := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("start-fullscreen", opts.Kiosk),
		chromedp.Flag("kiosk", opts.Kiosk),
		chromedp.Flag("headless", false),
		chromedp.Flag("enable-automation", false),
	)

	allocCtx, cancelAllocator := chromedp.NewExecAllocator(context.Background(), allocatorOptions...)

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

	kiosk := NewKiosk()

	// first tab is special
	rootContext, cancelContext := chromedp.NewContext(
		allocCtx,
		chromedp.WithLogf(func(msg string, values ...interface{}) {
			log.Printf(msg, values...)
		}),
	)

	if opts.Verbose {
		log.Printf("Performing actions for tab %s\n", tabs[0])

		for _, a := range tabs[0].Steps {
			log.Printf("  * %s\n", a)
		}
	}

	err = chromedp.Run(rootContext, tabs[0].Actions()...)

	if err != nil {
		log.Fatalf("Could not create tab '%v': %v", tabs[0].Name, err)
	}

	err = saveScreenshot(rootContext, chromedp.FromContext(rootContext).Target.TargetID, kiosk.Images)

	if err != nil {
		log.Fatalf("Could not take screenshot of tab '%v': %v", tabs[0].Name, err)
	}

	kiosk.AppendContext(rootContext)

	// all other tabs are equal
	for _, tab := range tabs[1:] {
		if opts.Verbose {
			log.Printf("Performing actions for tab %s:\n", tab)

			for _, a := range tab.Steps {
				log.Printf("  * %s\n", a)
			}
		}

		ctx, err := newTab(rootContext, tab.Actions()...)

		if err != nil {
			log.Fatalf("Could not create tab '%v': %v", tab.Name, err)
		}

		err = saveScreenshot(ctx, chromedp.FromContext(ctx).Target.TargetID, kiosk.Images)

		if err != nil {
			log.Fatalf("Could not take screenshot of tab '%v': %v", tabs[0].Name, err)
		}

		kiosk.AppendContext(ctx)
	}

	quitTabSwitcher := make(chan struct{})
	go switchTabsForever(kiosk, quitTabSwitcher)
	err = configureMqtt(kiosk, quitTabSwitcher)

	if err != nil {
		log.Fatalf("Could not connect to MQTT: %s", err)
	}

	quitProgram := make(chan struct{})
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		cancelAllocator()
		cancelContext()
		close(quitProgram)
	}()

	http.Handle("/", createRootHandler(kiosk))
	http.Handle("/image/", createImageHandler(kiosk))

	go func() {
		log.Println("Server started at port 8080")
		log.Fatal(http.ListenAndServe(":8080", nil))
	}()

	<-quitProgram
}

func Activate(parentContext context.Context, targetID target.ID) error {
	err := chromedp.Run(parentContext, chromedp.ActionFunc(func(ctx context.Context) error {
		err := target.ActivateTarget(targetID).Do(ctx)

		if err != nil {
			return err
		}

		return nil
	}))

	if err != nil {
		fmt.Println(err)
	}

	return nil
}

func newTab(parent context.Context, actions ...chromedp.Action) (context.Context, error) {
	ctx, _ := chromedp.NewContext(parent)

	err := chromedp.Run(ctx, actions...)

	if err != nil {
		return nil, err
	}

	return ctx, nil
}

func reverse(input interface{}) {
	inputLen := reflect.ValueOf(input).Len()
	inputMid := inputLen / 2
	inputSwap := reflect.Swapper(input)

	for i := 0; i < inputMid; i++ {
		j := inputLen - i - 1

		inputSwap(i, j)
	}
}

func getProgramName() string {
	path, err := os.Executable()

	if err != nil {
		fmt.Fprintln(os.Stderr, "Warning: Could not determine program name; using 'unknown'.")
		return "unknown"
	}

	return filepath.Base(path)
}

func switchToTab(kiosk *Kiosk, targetContext context.Context) error {
	targetID := chromedp.FromContext(targetContext).Target.TargetID

	// TODO do we really need the ActionFunc?
	err := chromedp.Run(kiosk.RootContext(), chromedp.ActionFunc(func(ctx context.Context) error {
		err := target.ActivateTarget(targetID).Do(ctx)

		if err != nil {
			return err
		}

		return nil
	}))

	if err != nil {
		return err
	}

	err = saveScreenshot(targetContext, targetID, kiosk.Images)

	if err != nil {
		return err
	}

	kiosk.SetCurrentTab(targetID)

	return nil
}

func saveScreenshot(ctx context.Context, targetID target.ID, images map[target.ID]*Image) error {
	var buf []byte

	// Chrome waits for the page described by ctx to be _active_
	if err := chromedp.Run(ctx, chromedp.CaptureScreenshot(&buf)); err != nil {
		return err
	}

	img, found := images[targetID]

	if !found {
		img = &Image{}
		images[targetID] = img
	}

	img.Store(buf)

	return nil
}

func switchTabsForever(kiosk *Kiosk, quitTabSwitcher chan struct{}) error {
	ticker := time.NewTicker(opts.Interval)

	if opts.Verbose {
		log.Println("Starting tab switching")
	}

	for {
		select {
		case <-ticker.C:
			nextContext, err := findNextTab(kiosk, true)

			if err != nil {
				return err
			}

			err = switchToTab(kiosk, nextContext)

			if err != nil {
				return err
			}
		case <-quitTabSwitcher:
			ticker.Stop()

			if opts.Verbose {
				log.Println("Stopping tab switching")
			}

			return nil
		}
	}
}

func findNextTab(kiosk *Kiosk, forward bool) (context.Context, error) {
	if !forward {
		reverse(kiosk.AllContexts)
	}

	for i, ctx := range kiosk.AllContexts {
		targetID := chromedp.FromContext(ctx).Target.TargetID

		// is this the current tab?
		if kiosk.CurrentTab == "" || targetID == kiosk.CurrentTab {
			// grab the context of the next tab or cycle to the beginning
			if i == len(kiosk.AllContexts)-1 {
				return kiosk.RootContext(), nil
			} else {
				return kiosk.AllContexts[i+1], nil
			}
		}
	}

	return nil, fmt.Errorf("could not find the current tab %v", kiosk.CurrentTab)
}

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
	}

	return false
}

func configureMqtt(kiosk *Kiosk, quitTabSwitcher chan struct{}) error {
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

	mqttOpts.OnConnect = createConnectHandler(kiosk, mqttURL, quitTabSwitcher)

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

func createConnectHandler(kiosk *Kiosk, mqttURL *url.URL, quitTabSwitcher chan struct{}) func(mqtt.Client) {
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
				pauseTabSwitching(quitTabSwitcher)
			case "resume":
				pauseTabSwitching(quitTabSwitcher)
				quitTabSwitcher = make(chan struct{})
				go switchTabsForever(kiosk, quitTabSwitcher)
			case "next":
				pauseTabSwitching(quitTabSwitcher)

				nextContext, err := findNextTab(kiosk, true)

				if err != nil {
					log.Println(err)
					return
				}

				err = switchToTab(kiosk, nextContext)

				if err != nil {
					log.Println(err)
					return
				}
			case "previous":
				pauseTabSwitching(quitTabSwitcher)

				previousContext, err := findNextTab(kiosk, false)

				if err != nil {
					log.Println(err)
					return
				}

				err = switchToTab(kiosk, previousContext)

				if err != nil {
					log.Println(err)
					return
				}
			default:
				log.Printf("Could not interpret MQTT command '%v'\n", command)
			}
		})

		if !token.WaitTimeout(10 * time.Second) {
			log.Fatalf("Could not subscribe: %v", token.Error())
		}
	}
}

func pauseTabSwitching(quitTabSwitcher chan struct{}) {
	if !isClosed(quitTabSwitcher) {
		close(quitTabSwitcher)
	}
}

func createRootHandler(kiosk *Kiosk) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")

		fmt.Fprintf(w, "<ul>\n")

		for id := range kiosk.Images {
			fmt.Fprintf(w, `<li><img src="/image/%v"/>`, id)
			fmt.Fprintln(w)
		}

		fmt.Fprintf(w, "</ul>\n")
	}
}

func createImageHandler(kiosk *Kiosk) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		targetID := target.ID(strings.TrimPrefix(r.URL.Path, "/image/"))

		img, found := kiosk.Images[targetID]

		if !found {
			http.NotFound(w, r)
			fmt.Fprintf(w, "No image for target ID %v", targetID)
			return
		}

		w.Header().Set("Content-Type", "image/png")
		w.Write(img.Get())
	}
}
