// based on https://pkg.go.dev/github.com/chromedp/chromedp#example-ExecAllocator
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"reflect"
	"syscall"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/jessevdk/go-flags"
	"uhlig.it/kiosk/script"
)

func stdErrLogger(msg string, values ...interface{}) {
	fmt.Fprintln(os.Stderr, msg, values)
}

var opts struct {
	Verbose  bool          `short:"v" long:"verbose" description:"Print verbose information"`
	Kiosk    bool          `short:"k" long:"kiosk" description:"Run in kiosk mode"`
	Interval time.Duration `short:"i" long:"interval" description:"how long to wait before switching to the next tab. Anything Go's time#ParseDuration understands is accepted." default:"5s"`
	Args     struct {
		Scriptfile string
	} `positional-args:"yes" required:"yes"`
}

func main() {
	_, err := flags.Parse(&opts)

	if err != nil {
		os.Exit(1)
	}

	allocatorOptions := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("start-fullscreen", opts.Kiosk),
		chromedp.Flag("kiosk", opts.Kiosk),
		chromedp.Flag("headless", false),
		chromedp.Flag("enable-automation", false),
		// chromedp.Flag("hide-scrollbars", true),
		// chromedp.Flag("noerrdialogs", true),
		// chromedp.Flag("disable-session-crashed-bubble", true),
		// chromedp.Flag("simulate-outdated-no-au", "Tue, 31 Dec 2099 23:59:59 GMT"),
		// chromedp.Flag("disable-component-update", true),
		// chromedp.Flag("disable-translate", true),
		// chromedp.Flag("disable-infobars", true),
		// chromedp.Flag("disable-features", "Translate"),
		// chromedp.Flag("disk-cache-dir", "/dev/null"),
	)

	allocCtx, cancelAllocator := chromedp.NewExecAllocator(context.Background(), allocatorOptions...)

	scriptBytes, err := os.ReadFile(opts.Args.Scriptfile)

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading scriptfile %v: %v", opts.Args.Scriptfile, err)
		os.Exit(1)
	}

	tabs, err := script.Parse(scriptBytes)

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing scriptfile %v: %v", opts.Args.Scriptfile, err)
		os.Exit(1)
	}

	// first tab is special
	windowCtx, cancelContext := chromedp.NewContext(allocCtx, chromedp.WithLogf(stdErrLogger))

	if opts.Verbose {
		log.Printf("Performing actions for tab %s\n", tabs[0])

		for _, a := range tabs[0].Steps {
			log.Printf("  * %s\n", a)
		}
	}

	err = chromedp.Run(windowCtx, tabs[0].Actions()...)

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var ctxe []*context.Context // keep all contexts in scope

	// all other tabs are equal
	for _, tab := range tabs[1:] {
		if opts.Verbose {
			log.Printf("Performing actions for tab %s:\n", tab)

			for _, a := range tab.Steps {
				log.Printf("  * %s\n", a)
			}
		}

		ctx, err := newTab(&windowCtx, tab.Actions()...)

		if err != nil {
			log.Println(err)
		} else {
			ctxe = append(ctxe, ctx)
		}
	}

	ticker := time.NewTicker(opts.Interval)
	quitTicker := make(chan struct{})
	go func() {
		var (
			target *target.Info
			err    error
		)

		if opts.Verbose {
			log.Printf("Switching tabs every %v\n", opts.Interval)
		}

		for {
			select {
			case <-ticker.C:
				target, err = switchToNextTab(windowCtx, target)

				if err != nil {
					fmt.Fprintf(os.Stderr, "Error switching to next tab: %v", err)
				}
			case <-quitTicker:
				ticker.Stop()
				return
			}
		}
	}()

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

	<-quitProgram
}

func switchToNextTab(ctx context.Context, currentPage *target.Info) (*target.Info, error) {
	targets, err := chromedp.Targets(ctx)

	if err != nil {
		log.Fatalln(err)
	}

	var pageTargets []*target.Info

	for _, t := range targets {
		if t.Type == "page" {
			pageTargets = append(pageTargets, t)
		}
	}

	reverse(pageTargets) // still not quite in the order we created the tabs

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

				log.Printf("Activating :%v (%v)\n", pageToBeActivated.URL, pageToBeActivated.TargetID)
			}

			err := Activate(ctx, pageToBeActivated.TargetID)

			if err != nil {
				return nil, err
			}

			return pageToBeActivated, nil
		}
	}

	return currentPage, nil // no change
}

func Activate(ctx context.Context, targetID target.ID) error {
	err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctxt context.Context) error {
		err := target.ActivateTarget(targetID).Do(ctxt)
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

func newTab(windowCtx *context.Context, actions ...chromedp.Action) (*context.Context, error) {
	ctx, _ := chromedp.NewContext(*windowCtx)

	err := chromedp.Run(ctx, actions...)

	if err != nil {
		return nil, err
	}

	return &ctx, nil
}

func remove(slice []*context.Context, s int) []*context.Context {
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
