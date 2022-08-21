// based on https://pkg.go.dev/github.com/chromedp/chromedp#example-ExecAllocator
package main

/*
Disabling Chromium's password manager seems not possible in preferences, only by policy:

cat /etc/chromium/policies/managed/no-password-management.json
{
	"AutofillAddressEnabled": false,
	"AutofillCreditCardEnabled": false,
	"PasswordManagerEnabled": false
}

old: "AutoFillEnabled": false,

// from https://stackoverflow.com/a/55316111/3212907
*/

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/chromedp/chromedp"
)

func main() {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false),
		chromedp.Flag("hide-scrollbars", true),
		chromedp.Flag("enable-automation", false),
		chromedp.Flag("start-fullscreen", true),
		chromedp.Flag("kiosk", true),
		chromedp.Flag("noerrdialogs", true),
		chromedp.Flag("disable-session-crashed-bubble", true),
		chromedp.Flag("simulate-outdated-no-au", "Tue, 31 Dec 2099 23:59:59 GMT"),
		chromedp.Flag("disable-component-update", true),
		chromedp.Flag("disable-translate", true),
		chromedp.Flag("disable-infobars", true),
		chromedp.Flag("disable-features", "Translate"),
		chromedp.Flag("disk-cache-dir", "/dev/null"),
	)

	allocCtx, cancelAllocator := chromedp.NewExecAllocator(context.Background(), opts...)

	// also set up a custom logger
	taskCtx, cancelContext := chromedp.NewContext(allocCtx, chromedp.WithLogf(log.Printf))

	user := os.Getenv("KIOSK_USER")

	if user == "" {
		fmt.Fprintln(os.Stderr, "Environment variable KIOSK_USER must not be empty")
		os.Exit(1)
	}

	password := os.Getenv("KIOSK_PASSWORD")

	if password == "" {
		fmt.Fprintln(os.Stderr, "Environment variable KIOSK_PASSWORD must not be empty")
		os.Exit(1)
	}

	actions := []chromedp.Action{
		chromedp.Navigate(`https://grafana.uhlig.it/login`),
		typeText(`//input[@name="user"]`, user),
		typeText(`//input[@name="password"]`, password),
		chromedp.Click(`//button[@type='submit']`, chromedp.NodeVisible),
		chromedp.WaitVisible(`//a[@href='/profile']`),
		chromedp.Navigate(`https://grafana.uhlig.it/d/yP_VJJmVz/power?refresh=30s&from=now-1h&to=now&kiosk`),
	}

	err := chromedp.Run(taskCtx, actions...)

	if err != nil {
		log.Fatal(err)
	}

	var quit = make(chan struct{})

	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		cancelAllocator()
		cancelContext()
		close(quit)
	}()

	<-quit
}

func typeText(selector, value string) chromedp.Tasks {
	return chromedp.Tasks{
		chromedp.WaitVisible(selector),
		chromedp.SendKeys(selector, value),
	}
}
