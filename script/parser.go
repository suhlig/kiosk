package script

import (
	"fmt"

	"gopkg.in/yaml.v2"
)

func Parse(markup []byte) ([]*Tab, error) {
	var tabs []*Tab

	err := yaml.Unmarshal(markup, &tabs)

	if err != nil {
		return nil, err
	}

	for _, tab := range tabs {
		for _, rawSteps := range tab.RawSteps {
			var step Step

			for typ, value := range rawSteps {
				switch typ {
				case "go":
					goStep, ok := value.(string)
					if !ok {
						err = fmt.Errorf("unable to parse '%v' as value of a Go step", value)
					} else {
						step = Go(goStep)
					}
				case "wait":
					waitStep, ok := value.(string)
					if !ok {
						err = fmt.Errorf("unable to parse '%v' as value of a Wait step", value)
					} else {
						step = Wait(waitStep)
					}
				case "click":
					clickStep, ok := value.(string)
					if !ok {
						err = fmt.Errorf("unable to parse '%v' as value of a Click step", value)
					} else {
						step = Click(clickStep)
					}
				case "type":
					typeAttributes, ok := value.(map[interface{}]interface{})

					if !ok {
						err = fmt.Errorf("unable to parse '%v' as value of a Type step", value)
					} else {
						var typeStep Type

						for k, v := range typeAttributes {
							switch k {
							case "xpath":
								tt, ok := v.(string)

								if ok {
									typeStep.XPath = tt
								} else {
									err = fmt.Errorf("unable to convert '%v' as 'xpath' value of a Type step", v)
								}
							case "value":
								tt, ok := v.(string)

								if ok {
									typeStep.Value = tt
								} else {
									err = fmt.Errorf("unable to convert '%v' as 'value' value of a Type step", v)
								}
							case "secret":
								tt, ok := v.(string)

								if ok {
									typeStep.Secret = tt
								} else {
									err = fmt.Errorf("unable to convert '%v' as 'secret' value of a Type step", v)
								}
							default:
								err = fmt.Errorf("'%v' is not a known key for a Type step", k)
							}
						}

						step = &typeStep
					}
				default:
					err = fmt.Errorf("'%v' is not a known step", typ)
				}
			}

			if err != nil {
				return nil, err
			}

			validationError := step.Validate()

			if validationError != nil {
				return nil, validationError
			}

			tab.Steps = append(tab.Steps, step)
		}
	}

	return tabs, nil
}
