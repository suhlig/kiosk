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

	var ctxe []context.Context

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

	// TODO save screenshot right after creating the tab

	ctxe = append(ctxe, rootContext)

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

		// TODO save screenshot right after creating the tab

		ctxe = append(ctxe, ctx)
	}

	images := make(map[target.ID]*Image)
	quitTabSwitcher := make(chan struct{})
	go switchTabsForever(ctxe, quitTabSwitcher, images)

	err = configureMqtt(rootContext, quitTabSwitcher)

	if err != nil {
		log.Fatalf("Could not connect to MQTT: %s", err)
	}

	quitProgram := make(chan struct{})
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		// not sure if this is needed...
		for i := 0; i < len(ctxe); i++ {
			ctxe = remove(ctxe, i)
		}
		cancelAllocator()
		cancelContext()
		close(quitProgram)
	}()

	http.Handle("/", createRootHandler(images))
	http.Handle("/image/", createImageHandler(images))

	go func() {
		log.Println("Server started at port 8080")
		log.Fatal(http.ListenAndServe(":8080", nil))
	}()

	<-quitProgram
}

func switchToNextTabOld(rootContext context.Context, currentPage *target.Info, forward bool) (*target.Info, error) {
	pageTargets, err := getPages(rootContext)

	if err != nil {
		log.Fatalln(err)
	}

	if forward {
		reverse(pageTargets) // still not quite in the order we created the tabs
	}

	for i, p := range pageTargets {
		if currentPage == nil || p.TargetID == currentPage.TargetID {
			var pageToBeActivated *target.Info

			if i == len(pageTargets)-1 {
				pageToBeActivated = pageTargets[0]
			} else {
				pageToBeActivated = pageTargets[i+1]
			}

			if opts.Verbose {
				if currentPage != nil {
					log.Printf("Currently active: %v (%v)\n", currentPage.URL, currentPage.TargetID)
				}

				log.Printf("Activating %v (%v)\n", pageToBeActivated.URL, pageToBeActivated.TargetID)
			}

			err := Activate(rootContext, pageToBeActivated.TargetID)

			if err != nil {
				return nil, err
			}

			return pageToBeActivated, nil
		}
	}

	return currentPage, nil // no change
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

func remove(slice []context.Context, s int) []context.Context {
	return append(slice[:s], slice[s+1:]...)
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

func switchToTab(rootContext, targetContext context.Context, images map[target.ID]*Image) (target.ID, error) {
	targetID := chromedp.FromContext(targetContext).Target.TargetID

	// works, but do we really need the ActionFunc?
	err := chromedp.Run(rootContext, chromedp.ActionFunc(func(ctx context.Context) error {
		err := target.ActivateTarget(targetID).Do(ctx)

		if err != nil {
			return err
		}

		return nil
	}))

	if err != nil {
		return "", err
	}

	var buf []byte

	// the key thing seems to be that Chrome waits for the page described by ctx to be _active_
	if err := chromedp.Run(targetContext, chromedp.CaptureScreenshot(&buf)); err != nil {
		return "", err
	} else {
		img, found := images[targetID]

		if !found {
			img = &Image{}
			images[targetID] = img
		}

		img.Store(buf)

		if opts.Verbose {
			log.Printf("stored image of tab %v\n", targetID)
		}
	}

	return targetID, nil
}

func switchTabsForever(ctxe []context.Context, quitTicker chan struct{}, images map[target.ID]*Image) error {
	var (
		currentTab  target.ID
		err         error
		nextContext context.Context
	)

	for range time.NewTicker(opts.Interval).C {
		for i, ctx := range ctxe {
			targetID := chromedp.FromContext(ctx).Target.TargetID

			// is this the current tab?
			if currentTab == "" || targetID == currentTab {
				// grab the context of the next tab or cycle to the beginning
				if i == len(ctxe)-1 {
					nextContext = ctxe[0]
				} else {
					nextContext = ctxe[i+1]
				}
			}
		}

		currentTab, err = switchToTab(ctxe[0], nextContext, images)

		if err != nil {
			return err
		}
	}

	return nil
}

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
	}

	return false
}

func getPages(ctx context.Context) (pageTargets []*target.Info, err error) {
	targets, err := chromedp.Targets(ctx)

	if err != nil {
		return
	}

	for _, t := range targets {
		if t.Type == "page" {
			pageTargets = append(pageTargets, t)
		}
	}

	return
}

func configureMqtt(rootContext context.Context, quitTabSwitcher chan struct{}) error {
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

	mqttOpts.OnConnect = onConnectFunc(mqttURL, rootContext, quitTabSwitcher)

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

func onConnectFunc(mqttURL *url.URL, rootContext context.Context, quitTabSwitcher chan struct{}) func(mqtt.Client) {
	var (
		target *target.Info
		err    error
	)

	topic := strings.TrimPrefix(mqttURL.Path, "/")

	return func(mqttClient mqtt.Client) {
		if opts.Verbose {
			log.Printf("Connected to MQTT at %v\n", mqttURL.Host)
		}

		if opts.Verbose {
			log.Printf("Subscribing to %v\n", topic)
		}

		token := mqttClient.Subscribe(topic, 0, func(c mqtt.Client, m mqtt.Message) {
			message := string(m.Payload())

			if opts.Verbose {
				log.Printf("Received message '%v'\n", message)
			}

			switch message {
			case "pause":
				if !isClosed(quitTabSwitcher) {
					close(quitTabSwitcher)
				}
			case "next":
				if !isClosed(quitTabSwitcher) {
					close(quitTabSwitcher)
				}

				target, err = switchToNextTabOld(rootContext, target, true)

				if err != nil {
					fmt.Fprintf(os.Stderr, "Error switching to next tab: %v\n", err)
				}
			case "previous":
				if !isClosed(quitTabSwitcher) {
					close(quitTabSwitcher)
				}

				target, err = switchToNextTabOld(rootContext, target, false)

				if err != nil {
					fmt.Fprintf(os.Stderr, "Error switching to next tab: %v\n", err)
				}
			case "resume":
				// TODO

				// if !isClosed(quitTabSwitcher) {
				// 	close(quitTabSwitcher)
				// }

				// // perform the next switch immediately
				// target, err = switchToNextTab(rootContext, target, false)

				// if err != nil {
				// 	fmt.Fprintf(os.Stderr, "Error switching to next tab: %v\n", err)
				// }

				// quitTabSwitcher = make(chan struct{})
				// go switchTabs(rootContext, quitTabSwitcher)
			default:
				log.Printf("Could not interpret MQTT command '%v'\n", message)
			}

		})

		if !token.WaitTimeout(10 * time.Second) {
			log.Fatalf("Could not subscribe: %v", token.Error())
		}
	}
}

func createRootHandler(images map[target.ID]*Image) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")

		fmt.Fprintf(w, "<ul>\n")

		for id := range images {
			fmt.Fprintf(w, `<li><img src="/image/%v"/>`, id)
			fmt.Fprintln(w)
		}

		fmt.Fprintf(w, "</ul>\n")
	}
}

func createImageHandler(images map[target.ID]*Image) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		targetID := target.ID(strings.TrimPrefix(r.URL.Path, "/image/"))

		img, found := images[targetID]

		if !found {
			http.NotFound(w, r)
			fmt.Fprintf(w, "No image for target ID %v", targetID)
			return
		}

		w.Header().Set("Content-Type", "image/png")
		w.Write(img.Get())
	}
}
