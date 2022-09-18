package main

import (
	"embed"
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
	log.SetFlags(0) // no timestamp etc. - we have systemd's timestamps in the log anyway

	_, err := flags.Parse(&opts)

	if err != nil {
		log.Fatal(err)
	}

	if opts.Version {
		log.Printf(getProgramVersion())
		os.Exit(0)
	}

	if opts.Verbose {
		log.Printf("MAIN Starting with options: %v\n", opts)
	}

	var scriptBytes []byte

	if opts.Args.Scriptfile == "" {
		if opts.Verbose {
			log.Println("MAIN Reading script from STDIN")
		}
		scriptBytes, err = io.ReadAll(os.Stdin)
	} else {
		if opts.Verbose {
			log.Printf("MAIN Reading script from %v\n", opts.Args.Scriptfile)
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

	kiosk := controller.NewKiosk().
		WithInterval(opts.Interval).
		WithFullScreen(opts.Kiosk).
		WithHeadless(opts.Headless)

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

func getProgramVersion() string {
	return fmt.Sprintf("%s %s (%s), built on %s\n", getProgramName(), version, commit, date)
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
		tmpl.Execute(w, map[string]any{
			"programVersion": getProgramVersion(),
			"images":         kiosk.ImageIDs(),
			"isSwitching":    kiosk.IsTabSwitching(),
		})
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
			http.Error(w, "Could not switch to tab", http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, fmt.Sprintf("/#%v", targetID), http.StatusTemporaryRedirect)
	}
}
