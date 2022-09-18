package controller

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

type StatusUpdate struct {
	IsTabSwitching bool   `json:"isTabSwitching"`
	CurrentTab     string `json:"currentTab"`
}

type Kiosk struct {
	StatusUpdates    chan StatusUpdate
	currentTab       target.ID
	allContexts      []context.Context
	images           map[target.ID]*Image
	quitTabSwitching chan struct{}
	interval         time.Duration
	fullScreen       bool
	headless         bool
	cancelAllocator  context.CancelFunc
	cancelContext    context.CancelFunc
	extraFlags       map[string]interface{}
}

func NewKiosk() *Kiosk {
	return &Kiosk{
		images:        make(map[target.ID]*Image),
		extraFlags:    make(map[string]interface{}),
		StatusUpdates: make(chan StatusUpdate, 10),
	}
}

func (k *Kiosk) WithInterval(interval time.Duration) *Kiosk {
	k.interval = interval
	return k
}

func (k *Kiosk) WithFullScreen(fullScreen bool) *Kiosk {
	k.fullScreen = fullScreen
	return k
}

func (k *Kiosk) WithHeadless(headless bool) *Kiosk {
	k.headless = headless
	return k
}

func (k *Kiosk) WithFlag(key string, value interface{}) *Kiosk {
	k.extraFlags[key] = value
	return k
}

func (k *Kiosk) NewTab(tab *script.Tab) error {
	if len(k.allContexts) == 0 {
		return k.createFirstTab(tab)
	}

	return k.createAdditionalTab(tab)
}

func (k *Kiosk) NextTab() error {
	k.PauseTabSwitching()

	nextContext, err := k.findNextTab(true)

	if err != nil {
		return err
	}

	err = k.switchToTab(nextContext)

	if err != nil {
		return err
	}

	return nil
}

func (k *Kiosk) PreviousTab() error {
	k.PauseTabSwitching()

	previousContext, err := k.findNextTab(false)

	if err != nil {
		return err
	}

	err = k.switchToTab(previousContext)

	if err != nil {
		return err
	}

	return nil
}

func (k *Kiosk) SwitchToTab(targetID string) error {
	k.PauseTabSwitching()

	nextContext, err := k.findTab(target.ID(targetID))

	if err != nil {
		return err
	}

	return k.switchToTab(nextContext)
}

func (k *Kiosk) StartTabSwitching() {
	k.quitTabSwitching = make(chan struct{})
	go k.switchTabsForever()

	k.StatusUpdates <- StatusUpdate{
		IsTabSwitching: k.IsTabSwitching(),
	}
}

func (k *Kiosk) PauseTabSwitching() {
	if !isClosed(k.quitTabSwitching) {
		close(k.quitTabSwitching)
	}

	k.StatusUpdates <- StatusUpdate{
		IsTabSwitching: k.IsTabSwitching(),
	}
}

func (k *Kiosk) IsTabSwitching() bool {
	return !isClosed(k.quitTabSwitching)
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
		chromedp.Flag("headless", k.headless),
		chromedp.Flag("enable-automation", false),
	)

	for key, value := range k.extraFlags {
		allocatorOptions = append(allocatorOptions, chromedp.Flag(key, value))
	}

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
	k.StatusUpdates <- StatusUpdate{
		IsTabSwitching: k.IsTabSwitching(),
		CurrentTab:     id.String(),
	}
}

func (k *Kiosk) switchTabsForever() error {
	ticker := time.NewTicker(k.interval)

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
