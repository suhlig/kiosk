package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/template"
	"time"

	"github.com/jessevdk/go-flags"
	"uhlig.it/kiosk/controller"
	"uhlig.it/kiosk/script"
	"uhlig.it/kiosk/videocore"
)

type options struct {
	Version         bool          `short:"V" long:"version" description:"Print version information and exit"`
	Verbose         bool          `short:"v" long:"verbose" description:"Print verbose information"`
	Kiosk           bool          `short:"k" long:"kiosk" description:"Run in kiosk mode"`
	Headless        bool          `short:"H" long:"headless" description:"Run headless"`
	Interval        time.Duration `short:"i" long:"interval" description:"how long to wait before switching to the next tab. Anything Go's time#ParseDuration understands is accepted." default:"5s"`
	HttpBindAddress string        `short:"a" long:"http-address" description:"Address to bind the HTTP control server to" default:"localhost:8011"`
	ChromeFlags     []string      `long:"chrome-flag" description:"additional flags to pass to chromium"`
	Args            struct {
		Scriptfile string
	} `positional-args:"yes"`
}

func (o options) String() string {
	return fmt.Sprintf(`kiosk: %v, headless: %v, interval: %v, chromeflags: %v`, o.Kiosk, o.Headless, o.Interval, o.ChromeFlags)
}

// ldflags will be set by goreleaser
var version = "vDEV"
var commit = "NONE"
var date = "UNKNOWN"

var opts options

//go:embed *.html.tmpl
var htmlAssets embed.FS

func main() {
	logger := log.New(os.Stderr, "MAIN ", 0)

	_, err := flags.Parse(&opts)

	if err != nil {
		logger.Fatal(err)
	}

	if opts.Version {
		logger.Println(getProgramVersion())
		os.Exit(0)
	}

	if opts.Verbose {
		logger.Printf("starting with options: %v\n", opts)
	}

	var scriptBytes []byte

	if opts.Args.Scriptfile == "" {
		if opts.Verbose {
			log.Println("Reading script from STDIN")
		}
		scriptBytes, err = io.ReadAll(os.Stdin)
	} else {
		if opts.Verbose {
			log.Printf("Reading script from %v\n", opts.Args.Scriptfile)
		}
		scriptBytes, err = os.ReadFile(opts.Args.Scriptfile)
	}

	if err != nil {
		log.Fatalf("Could not read scriptfile: %v\n", err)
	}

	tabs, err := script.Parse(scriptBytes)

	if err != nil {
		log.Fatalf("Could not parse scriptfile %v: %v\n", opts.Args.Scriptfile, err)
	}

	statusUpdates := make(chan controller.StatusUpdate, 10)

	kiosk := controller.NewKiosk().
		WithInterval(opts.Interval).
		WithFullScreen(opts.Kiosk).
		WithHeadless(opts.Headless).
		WithStatusUpdates(statusUpdates)

	for _, cf := range opts.ChromeFlags {
		key, value, found := strings.Cut(cf, "=")

		if !found {
			log.Fatalf("Could not separate chrome flag %v; expecting k=v\n", cf)
		}

		kiosk = kiosk.WithFlag(key, value)
	}

	for _, tab := range tabs {
		if opts.Verbose {
			log.Printf("Performing actions for tab %s:\n", tab)

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
		logger.Println("starting tab switching")
	}

	kiosk.StartTabSwitching()

	quitProgram := make(chan struct{})
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		kiosk.Close()
		close(quitProgram)
	}()

	weblogger := log.New(os.Stderr, "WEB ", 0)

	http.Handle("/", createRootHandler(kiosk, weblogger))
	http.Handle("/image/", createImageHandler(kiosk, weblogger))
	http.Handle("/activate/", createActivateHandler(kiosk, weblogger))
	http.Handle("/pause", createPauseHandler(kiosk, weblogger))
	http.Handle("/resume", createResumeHandler(kiosk, weblogger))
	http.Handle("/updates", createUpdateHandler(kiosk, weblogger, statusUpdates))
	http.Handle("/backlight", createBacklightHandlers(weblogger, statusUpdates))

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

func getProgramVersion() string {
	return fmt.Sprintf("%s %s (%s), built on %s", getProgramName(), version, commit, date)
}

func createRootHandler(kiosk *controller.Kiosk, logger *log.Logger) http.HandlerFunc {
	tmpl, err := template.ParseFS(htmlAssets, "index.html.tmpl")

	if err != nil {
		logger.Fatal(err)
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "text/html")
		tmpl.Execute(w, map[string]any{
			"programVersion": getProgramVersion(),
			"images":         kiosk.ImageIDs(),
			"isTabSwitching": kiosk.IsTabSwitching(),
		})
	}
}

func createImageHandler(kiosk *controller.Kiosk, logger *log.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		imageID := strings.TrimPrefix(r.URL.Path, "/image/")

		img, found := kiosk.GetImage(imageID)

		if !found {
			msg := fmt.Sprintf("no image for target ID %v", imageID)
			logger.Println(msg)
			http.Error(w, msg, http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "image/png")
		w.Write(img.GetData())
	}
}

func createActivateHandler(kiosk *controller.Kiosk, logger *log.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error": "Only POST allowed here"}`, http.StatusMethodNotAllowed)
			return
		}

		if err := r.ParseForm(); err != nil {
			logger.Printf("could not parse form parameters: %v", err)
			http.Error(w, `{"error": "could not parse form parameters"}`, http.StatusUnprocessableEntity)
			return
		}

		targetID := r.FormValue("id")

		if opts.Verbose {
			logger.Printf("switching to tab %v", targetID)
		}

		err := kiosk.SwitchToTab(targetID)

		if err != nil {
			logger.Printf("could not switch to tab: %v", err)
			http.Error(w, `{"error": "could not switch to tab"}`, http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, fmt.Sprintf("/#%v", targetID), http.StatusTemporaryRedirect)
	}
}

func createPauseHandler(kiosk *controller.Kiosk, logger *log.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error": "Only POST allowed here"}`, http.StatusMethodNotAllowed)
			return
		}

		logger.Println("pausing tab switching")
		kiosk.PauseTabSwitching()
		w.WriteHeader(http.StatusCreated)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"isTabSwitching": %v}`, kiosk.IsTabSwitching())
	}
}

func createResumeHandler(kiosk *controller.Kiosk, logger *log.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error": "Only POST allowed here"}`, http.StatusMethodNotAllowed)
			return
		}

		logger.Println("resuming tab switching")
		kiosk.StartTabSwitching()
		w.WriteHeader(http.StatusCreated)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"isTabSwitching": %v}`, kiosk.IsTabSwitching())
	}
}

func createUpdateHandler(kiosk *controller.Kiosk, logger *log.Logger, statusUpdates chan controller.StatusUpdate) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		timeout := time.After(1 * time.Second)
		select {
		case event := <-statusUpdates:
			var buf bytes.Buffer
			enc := json.NewEncoder(&buf)
			enc.Encode(event)
			fmt.Fprintf(w, "data: %v\n\n", buf.String())
		case <-timeout:
			fmt.Fprintln(w, "UPDATES nothing to send")
		}

		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
}

func createBacklightHandlers(logger *log.Logger, statusUpdates chan controller.StatusUpdate) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			backlightGetHandler(w, r, logger, statusUpdates)
		case http.MethodPost:
			backlightPostHandler(w, r, logger, statusUpdates)
		default:
			http.Error(w, `{"error": "Only GET or POST allowed here"}`, http.StatusMethodNotAllowed)
			return
		}
	}
}

func backlightGetHandler(w http.ResponseWriter, r *http.Request, logger *log.Logger, statusUpdates chan controller.StatusUpdate) {
	displayStati, err := eachDisplay(func(id uint8) (bool, error) {
		return videocore.GetBacklight(id)
	})

	if err != nil {
		logger.Println(err)
		http.Error(w, `{"error": "unable to get display status"}`, http.StatusInternalServerError)
		return
	}

	update := controller.StatusUpdate{
		DisplayStati: displayStati,
	}

	statusUpdates <- update

	w.Header().Set("Content-Type", "application/json")
	err = json.NewEncoder(w).Encode(update)
	if err != nil {
		logger.Println(err)
		http.Error(w, `{"error": "unable to encode display status"}`, http.StatusInternalServerError)
		return
	}
}

func backlightPostHandler(w http.ResponseWriter, r *http.Request, logger *log.Logger, statusUpdates chan controller.StatusUpdate) {
	err := r.ParseForm()

	if err != nil {
		logger.Printf("could not parse form parameters: %v", err)
		http.Error(w, `{"error": "Could not parse form parameters"}`, http.StatusUnprocessableEntity)
		return
	}

	status := r.FormValue("status")
	logger.Printf("setting backlight of all displays to %v\n", status)

	var displayStati []*videocore.DisplayStatus

	switch status {
	case "0", "off", "false":
		displayStati, err = eachDisplay(func(id uint8) (bool, error) {
			return videocore.SetBacklight(id, false)
		})
	case "1", "on", "true":
		displayStati, err = eachDisplay(func(id uint8) (bool, error) {
			return videocore.SetBacklight(id, true)
		})
	case "toggle":
		displayStati, err = eachDisplay(func(id uint8) (bool, error) {
			return videocore.ToggleBacklight(id)
		})
	default:
		msg := fmt.Sprintf("unsupported status %v", status)
		logger.Println(msg)
		http.Error(w, fmt.Sprintf(`{"error": "%v"}`, msg), http.StatusInternalServerError)
		return
	}

	if err != nil {
		logger.Println(err)
		http.Error(w, `{"error": "unable to set display status"}`, http.StatusInternalServerError)
		return
	}

	update := controller.StatusUpdate{
		DisplayStati: displayStati,
	}

	statusUpdates <- update

	w.WriteHeader(http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")
	err = json.NewEncoder(w).Encode(update)

	if err != nil {
		logger.Println(err)
		http.Error(w, `{"error": "unable to encode display status"}`, http.StatusInternalServerError)
		return
	}
}

func eachDisplay(callback func(id uint8) (bool, error)) (displayStati []*videocore.DisplayStatus, err error) {
	displays, err := videocore.GetDisplays()

	if err != nil {
		return
	}

	for _, id := range displays {
		var status bool
		status, err = callback(id)

		if err != nil {
			return
		}

		displayStati = append(displayStati, &videocore.DisplayStatus{
			ID:     id,
			Status: status,
		})
	}

	return
}
