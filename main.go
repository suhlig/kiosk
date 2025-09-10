package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
	err := mainE()

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error %s\n", err)
		os.Exit(1)
	}
}

func mainE() error {
	rootLogger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	mainLogger := rootLogger.WithGroup("main")

	_, err := flags.Parse(&opts)

	if err != nil {
		return fmt.Errorf("unable to parse config: %w", err)
	}

	if opts.Version {
		fmt.Println(getProgramVersion())
		os.Exit(0)
	}

	if opts.Verbose {
		mainLogger.Info("starting with", "options", opts)
	}

	var scriptBytes []byte

	if opts.Args.Scriptfile == "" {
		if opts.Verbose {
			mainLogger.Info("Reading script from STDIN")
		}
		scriptBytes, err = io.ReadAll(os.Stdin)
	} else {
		if opts.Verbose {
			mainLogger.Info("Reading script", "script file", opts.Args.Scriptfile)
		}
		scriptBytes, err = os.ReadFile(opts.Args.Scriptfile)
	}

	if err != nil {
		return fmt.Errorf("could not read script file: %w", err)
	}

	tabs, err := script.Parse(scriptBytes)

	if err != nil {
		return fmt.Errorf("unable to parse script file: %w", err)
	}

	statusUpdates := make(chan controller.StatusUpdate, 10)

	kiosk := controller.NewKiosk().
		WithInterval(opts.Interval).
		WithFullScreen(opts.Kiosk).
		WithHeadless(opts.Headless).
		WithStatusUpdates(statusUpdates).
		WithLogger(rootLogger.WithGroup("kiosk"))

	for _, cf := range opts.ChromeFlags {
		key, value, found := strings.Cut(cf, "=")

		if !found {
			return fmt.Errorf("ould not separate chrome flag %v; expecting k=v", cf)
		}

		kiosk = kiosk.WithFlag(key, value)
	}

	for _, tab := range tabs {
		if opts.Verbose {
			mainLogger.Info("Performing actions for", "tab", tab)

			for _, step := range tab.Steps {
				mainLogger.Info("Performing", "step", step)
			}
		}

		err = kiosk.NewTab(tab)

		if err != nil {
			return fmt.Errorf("unable to open new tab: %w", err)
		}
	}

	if opts.Verbose {
		mainLogger.Info("starting tab switching")
	}

	kiosk.StartTabSwitching()

	weblogger := rootLogger.WithGroup("web")

	tmpl, err := template.ParseFS(htmlAssets, "index.html.tmpl")

	if err != nil {
		return fmt.Errorf("unable to parse main template")
	}

	http.Handle("/", createRootHandler(kiosk, tmpl, weblogger))
	http.Handle("/image/", createImageHandler(kiosk, weblogger))
	http.Handle("/activate/", createActivateHandler(kiosk, weblogger))
	http.Handle("/pause", createPauseHandler(kiosk, weblogger))
	http.Handle("/resume", createResumeHandler(kiosk, weblogger))
	http.Handle("/updates", createUpdateHandler(kiosk, weblogger, statusUpdates))
	http.Handle("/backlight", createBacklightHandlers(weblogger, statusUpdates))

	weblogger.Info("HTTP control server starting at", "address", opts.HttpBindAddress)
	return http.ListenAndServe(opts.HttpBindAddress, nil)
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

func createRootHandler(kiosk *controller.Kiosk, tmpl *template.Template, _ *slog.Logger) http.HandlerFunc {
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

func createImageHandler(kiosk *controller.Kiosk, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		imageID := strings.TrimPrefix(r.URL.Path, "/image/")

		img, found := kiosk.GetImage(imageID)

		if !found {
			msg := fmt.Sprintf("no image for target ID %v", imageID)
			logger.ErrorContext(r.Context(), msg)
			http.Error(w, msg, http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "image/png")
		w.Write(img.GetData())
	}
}

func createActivateHandler(kiosk *controller.Kiosk, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error": "Only POST allowed here"}`, http.StatusMethodNotAllowed)
			return
		}

		if err := r.ParseForm(); err != nil {
			logger.ErrorContext(r.Context(), "could not parse form parameters", "error", err.Error())
			http.Error(w, `{"error": "could not parse form parameters"}`, http.StatusUnprocessableEntity)
			return
		}

		targetID := r.FormValue("id")

		if opts.Verbose {
			logger.Info("switching to", "tab", targetID)
		}

		err := kiosk.SwitchToTab(targetID)

		if err != nil {
			logger.ErrorContext(r.Context(), "could not switch to tab", "tab", targetID, "error", err)
			http.Error(w, `{"error": "could not switch to tab"}`, http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, fmt.Sprintf("/#%v", targetID), http.StatusTemporaryRedirect)
	}
}

func createPauseHandler(kiosk *controller.Kiosk, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error": "Only POST allowed here"}`, http.StatusMethodNotAllowed)
			return
		}

		logger.ErrorContext(r.Context(), "pausing tab switching")
		kiosk.PauseTabSwitching()
		w.WriteHeader(http.StatusCreated)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"isTabSwitching": %v}`, kiosk.IsTabSwitching())
	}
}

func createResumeHandler(kiosk *controller.Kiosk, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error": "Only POST allowed here"}`, http.StatusMethodNotAllowed)
			return
		}

		logger.ErrorContext(r.Context(), "resuming tab switching")
		kiosk.StartTabSwitching()
		w.WriteHeader(http.StatusCreated)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"isTabSwitching": %v}`, kiosk.IsTabSwitching())
	}
}

func createUpdateHandler(_ *controller.Kiosk, _ *slog.Logger, statusUpdates chan controller.StatusUpdate) http.HandlerFunc {
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

func createBacklightHandlers(logger *slog.Logger, statusUpdates chan controller.StatusUpdate) http.HandlerFunc {
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

func backlightGetHandler(w http.ResponseWriter, r *http.Request, logger *slog.Logger, statusUpdates chan controller.StatusUpdate) {
	displayStati, err := eachDisplay(func(id uint8) (bool, error) {
		return videocore.GetBacklight(id)
	})

	if err != nil {
		logger.ErrorContext(r.Context(), "unable to get display status", "error", err)
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
		logger.ErrorContext(r.Context(), "unable to encode display status", "error", err)
		http.Error(w, `{"error": "unable to encode display status"}`, http.StatusInternalServerError)
		return
	}
}

func backlightPostHandler(w http.ResponseWriter, r *http.Request, logger *slog.Logger, statusUpdates chan controller.StatusUpdate) {
	err := r.ParseForm()

	if err != nil {
		logger.ErrorContext(r.Context(), "could not parse form parameters", "error", err)
		http.Error(w, `{"error": "Could not parse form parameters"}`, http.StatusUnprocessableEntity)
		return
	}

	status := r.FormValue("status")
	logger.Info("setting backlight of all displays to", "status", status)

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
		logger.ErrorContext(r.Context(), msg)
		http.Error(w, fmt.Sprintf(`{"error": "%v"}`, msg), http.StatusInternalServerError)
		return
	}

	if err != nil {
		logger.ErrorContext(r.Context(), err.Error())
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
		logger.ErrorContext(r.Context(), "unable to encode display status", "error", err.Error())
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
