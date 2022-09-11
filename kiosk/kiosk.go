package kiosk

import (
	"context"
	"fmt"
	"log"
	"reflect"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"uhlig.it/kiosk/script"
)

type Kiosk struct {
	currentTab       target.ID
	allContexts      []context.Context
	images           map[target.ID]*Image
	quitTabSwitching chan struct{}
	interval         time.Duration
	verbose          bool
	fullScreen       bool
	cancelAllocator  context.CancelFunc
	cancelContext    context.CancelFunc
}

func NewKiosk() *Kiosk {
	return &Kiosk{
		images: make(map[target.ID]*Image),
	}
}

func (k *Kiosk) WithInterval(interval time.Duration) *Kiosk {
	k.interval = interval
	return k
}

func (k *Kiosk) WithVerbose(verbose bool) *Kiosk {
	k.verbose = verbose
	return k
}

func (k *Kiosk) WithFullScreen(fullScreen bool) *Kiosk {
	k.fullScreen = fullScreen
	return k
}

func (k *Kiosk) NewTab(tab *script.Tab) error {
	if len(k.allContexts) == 0 {
		return k.createFirstTab(tab)
	}

	return k.createAdditionalTab(tab)
}

func (k *Kiosk) NextTab() {
	k.PauseTabSwitching()

	nextContext, err := k.findNextTab(true)

	if err != nil {
		log.Println(err)
		return
	}

	err = k.switchToTab(nextContext)

	if err != nil {
		log.Println(err)
		return
	}
}

func (k *Kiosk) PreviousTab() {
	k.PauseTabSwitching()

	previousContext, err := k.findNextTab(false)

	if err != nil {
		log.Println(err)
		return
	}

	err = k.switchToTab(previousContext)

	if err != nil {
		log.Println(err)
		return
	}
}

func (k *Kiosk) SwitchToTab(targetID string) error {
	k.PauseTabSwitching()

	nextContext, err := k.findTab(target.ID(targetID))

	if err != nil {
		return err
	}

	if k.verbose {
		log.Printf("Switching to tab %v\n", targetID)
	}

	return k.switchToTab(nextContext)
}

func (k *Kiosk) StartTabSwitching() {
	k.quitTabSwitching = make(chan struct{})
	go k.switchTabsForever()
}

func (k *Kiosk) PauseTabSwitching() {
	if !isClosed(k.quitTabSwitching) {
		close(k.quitTabSwitching)
	}
}

func (k *Kiosk) Close() {
	k.cancelAllocator()
	k.cancelContext()
}

func (k *Kiosk) GetImage(id string) (*Image, bool) {
	img, found := k.images[target.ID(id)]

	if found {
		return img, true
	}

	return nil, false
}

func (k *Kiosk) ImageIDs() (images []string) {
	for _, i := range k.images {
		images = append(images, i.GetID())
	}

	return
}

func (k *Kiosk) createFirstTab(tab *script.Tab) error {
	allocatorOptions := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("start-fullscreen", k.fullScreen),
		chromedp.Flag("kiosk", k.fullScreen),
		chromedp.Flag("headless", false),
		chromedp.Flag("enable-automation", false),
	)

	allocCtx, cancelAllocator := chromedp.NewExecAllocator(context.Background(), allocatorOptions...)

	k.cancelAllocator = cancelAllocator

	ctx, cancelContext := chromedp.NewContext(
		allocCtx,
		chromedp.WithLogf(func(msg string, values ...interface{}) {
			log.Printf(msg, values...)
		}),
	)

	k.cancelContext = cancelContext

	return k.createTab(ctx, tab)
}

func (k *Kiosk) createAdditionalTab(tab *script.Tab) error {
	ctx, _ := chromedp.NewContext(k.rootContext())
	return k.createTab(ctx, tab)
}

func (k *Kiosk) createTab(ctx context.Context, tab *script.Tab) error {
	if k.verbose {
		log.Printf("Performing actions for tab %s:\n", tab)

		for _, a := range tab.Steps {
			log.Printf("  * %s\n", a)
		}
	}

	err := chromedp.Run(ctx, tab.Actions()...)

	if err != nil {
		return fmt.Errorf("could not create tab '%v': %v", tab.Name, err)
	}

	err = k.saveScreenshot(ctx, chromedp.FromContext(ctx).Target.TargetID)

	if err != nil {
		return fmt.Errorf("could not take screenshot of tab '%v': %v", tab.Name, err)
	}

	k.allContexts = append(k.allContexts, ctx)

	return nil
}

func (k *Kiosk) rootContext() context.Context {
	return k.allContexts[0]
}

func (k *Kiosk) setCurrentTab(id target.ID) {
	(*k).currentTab = id
}

func (k *Kiosk) switchTabsForever() error {
	ticker := time.NewTicker(k.interval)

	if k.verbose {
		log.Println("Starting tab switching")
	}

	for {
		select {
		case <-ticker.C:
			nextContext, err := k.findNextTab(true)

			if err != nil {
				return err
			}

			err = k.switchToTab(nextContext)

			if err != nil {
				return err
			}
		case <-k.quitTabSwitching:
			ticker.Stop()

			if k.verbose {
				log.Println("Stopping tab switching")
			}

			return nil
		}
	}
}

func (k *Kiosk) switchToTab(targetContext context.Context) error {
	targetID := chromedp.FromContext(targetContext).Target.TargetID

	// TODO do we really need the ActionFunc?
	err := chromedp.Run(k.rootContext(), chromedp.ActionFunc(func(ctx context.Context) error {
		err := target.ActivateTarget(targetID).Do(ctx)

		if err != nil {
			return err
		}

		return nil
	}))

	if err != nil {
		return err
	}

	err = k.saveScreenshot(targetContext, targetID)

	if err != nil {
		return err
	}

	k.setCurrentTab(targetID)

	return nil
}

func (k *Kiosk) saveScreenshot(ctx context.Context, targetID target.ID) error {
	var buf []byte

	// Chrome waits for the page described by ctx to be _active_
	if err := chromedp.Run(ctx, chromedp.CaptureScreenshot(&buf)); err != nil {
		return err
	}

	img, found := k.images[targetID]

	if !found {
		img = &Image{}
		k.images[targetID] = img
	}

	img.Store(targetID.String(), buf)

	return nil
}

func (k *Kiosk) findNextTab(forward bool) (context.Context, error) {
	if !forward {
		reverse(k.allContexts)
	}

	for i, ctx := range k.allContexts {
		tabID := chromedp.FromContext(ctx).Target.TargetID

		// is this the current tab?
		if k.currentTab == "" || tabID == k.currentTab {
			// grab the context of the next tab or cycle to the beginning
			if i == len(k.allContexts)-1 {
				return k.rootContext(), nil
			} else {
				return k.allContexts[i+1], nil
			}
		}
	}

	return nil, fmt.Errorf("could not find the current tab %v", k.currentTab)
}

func (k *Kiosk) findTab(targetID target.ID) (context.Context, error) {
	for _, ctx := range k.allContexts {
		tabID := chromedp.FromContext(ctx).Target.TargetID

		if tabID == targetID {
			return ctx, nil
		}
	}

	return nil, fmt.Errorf("could not find the a tab with ID %v", targetID)
}

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
	}

	return false
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
