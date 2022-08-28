package script

import (
	"errors"
	"fmt"

	"github.com/chromedp/chromedp"
)

type Tab struct {
	Name     string                   `yaml:"name"`
	RawSteps []map[string]interface{} `yaml:"script"`
	Steps    []Step
}

func (n *Tab) Actions() []chromedp.Action {
	var actions []chromedp.Action

	for _, step := range n.Steps {
		actions = append(actions, step.Action())
	}

	return actions
}

func (n *Tab) String() string {
	return fmt.Sprintf("%v (%v actions)", n.Name, len(n.Actions()))
}

type Step interface {
	Action() chromedp.Action
	String() string
	Validate() error
}

type Go string

func (g Go) Action() chromedp.Action {
	return chromedp.Navigate(string(g))
}

func (g Go) String() string {
	return fmt.Sprintf("go to %v", string(g))
}

func (g Go) Validate() error {
	if g == "" {
		return errors.New("value must not be empty")
	}

	return nil
}

type Type struct {
	XPath  string `yaml:"xpath"`
	Value  string `yaml:"value"`
	Secret string `yaml:"secret"`
}

func (t *Type) Action() chromedp.Action {
	if t.Value != "" {
		return chromedp.SendKeys(t.XPath, t.Value)
	} else {
		return chromedp.SendKeys(t.XPath, t.Secret)
	}
}

func (t *Type) String() string {
	var value string

	if t.Value != "" {
		value = fmt.Sprintf("'%v'", t.Value)
	} else {
		value = "the secret"
	}

	return fmt.Sprintf("type %v into the element addressed by '%v'", value, t.XPath)
}

func (t *Type) Validate() error {
	if t.XPath == "" {
		return errors.New("value for xpath must not be empty")
	}

	if t.Value == "" && t.Secret == "" {
		return errors.New("either value or secret must be provided and not be empty")
	}

	return nil
}

type Click string

func (c Click) Action() chromedp.Action {
	return chromedp.Click(string(c), chromedp.NodeVisible)
}

func (c Click) String() string {
	return fmt.Sprintf("click the element addressed by '%v'", string(c))
}

func (c Click) Validate() error {
	if c == "" {
		return errors.New("value must not be empty")
	}

	return nil
}

type Wait string

func (w Wait) Action() chromedp.Action {
	return chromedp.WaitVisible(string(w))
}

func (w Wait) String() string {
	return fmt.Sprintf("wait for the element addressed by '%v'", string(w))

}

func (w Wait) Validate() error {
	if w == "" {
		return errors.New("value must not be empty")
	}

	return nil
}
