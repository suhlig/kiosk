package script_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"uhlig.it/kiosk/script"
)

var _ = Describe("Parser", func() {
	var scrpt []byte
	var err error
	var tabs []*script.Tab

	JustBeforeEach(func() {
		tabs, err = script.Parse(scrpt)
	})

	Context("invalid step script", func() {
		Context("Go", func() {
			Context("missing value", func() {
				BeforeEach(func() {
					scrpt = []byte(`
- name: Missing Value
  script:
    - wait: foo
    - go:
`)
				})

				It("does not parse", func() {
					Expect(err).To(HaveOccurred())
				})

				It("has the expected error", func() {
					Expect(err).To(MatchError("unable to parse '<nil>' as value of a Go step"))
				})

				It("has no tabs", func() {
					Expect(tabs).To(BeEmpty())
				})
			})

			Context("empty value", func() {
				BeforeEach(func() {
					scrpt = []byte(`
- name: Empty Value
  script:
    - wait: foo
    - go: ""
`)
				})

				It("does not parse", func() {
					Expect(err).To(HaveOccurred())
				})

				It("has the expected error", func() {
					Expect(err).To(MatchError("value must not be empty"))
				})

				It("has no tabs", func() {
					Expect(tabs).To(BeEmpty())
				})
			})
		})

		Context("Wait", func() {
			Context("missing value", func() {
				BeforeEach(func() {
					scrpt = []byte(`
- name: Missing Value
  script:
    - wait:
    - go: foo
`)
				})

				It("does not parse", func() {
					Expect(err).To(HaveOccurred())
				})

				It("has the expected error", func() {
					Expect(err).To(MatchError("unable to parse '<nil>' as value of a Wait step"))
				})

				It("has no tabs", func() {
					Expect(tabs).To(BeEmpty())
				})
			})

			Context("empty value", func() {
				BeforeEach(func() {
					scrpt = []byte(`
- name: Empty Value
  script:
    - wait: ""
    - go: foo
`)
				})

				It("does not parse", func() {
					Expect(err).To(HaveOccurred())
				})

				It("has the expected error", func() {
					Expect(err).To(MatchError("value must not be empty"))
				})

				It("has no tabs", func() {
					Expect(tabs).To(BeEmpty())
				})
			})
		})

		Context("Type", func() {
			Context("missing value", func() {
				BeforeEach(func() {
					scrpt = []byte(`
- name: Missing Value
  script:
    - type:
    - go: foo
`)
				})

				It("does not parse", func() {
					Expect(err).To(HaveOccurred())
				})

				It("has the expected error", func() {
					Expect(err).To(MatchError("unable to parse '<nil>' as value of a Type step"))
				})

				It("has no tabs", func() {
					Expect(tabs).To(BeEmpty())
				})
			})

			Context("wrong value type", func() {
				BeforeEach(func() {
					scrpt = []byte(`
- name: Empty Value
  script:
    - type: ""
    - go: foo
`)
				})

				It("does not parse", func() {
					Expect(err).To(HaveOccurred())
				})

				It("has the expected error", func() {
					Expect(err).To(MatchError("unable to parse '' as value of a Type step"))
				})

				It("has no tabs", func() {
					Expect(tabs).To(BeEmpty())
				})
			})

			Context("missing value in xpath", func() {
				BeforeEach(func() {
					scrpt = []byte(`
- name: Empty Value
  script:
    - type:
        xpath:
        value: not empty
    - go: foo
`)
				})

				It("does not parse", func() {
					Expect(err).To(HaveOccurred())
				})

				It("has the expected error", func() {
					Expect(err).To(MatchError("unable to convert '<nil>' as 'xpath' value of a Type step"))
				})

				It("has no tabs", func() {
					Expect(tabs).To(BeEmpty())
				})
			})

			Context("empty value in xpath", func() {
				BeforeEach(func() {
					scrpt = []byte(`
- name: Empty Value
  script:
    - type:
        xpath: ""
        value: not empty
    - go: foo
`)
				})

				It("does not parse", func() {
					Expect(err).To(HaveOccurred())
				})

				It("has the expected error", func() {
					Expect(err).To(MatchError("value for xpath must not be empty"))
				})

				It("has no tabs", func() {
					Expect(tabs).To(BeEmpty())
				})
			})

			Context("missing value in value", func() {
				BeforeEach(func() {
					scrpt = []byte(`
- name: Empty Value
  script:
    - type:
        xpath: not empty
        value:
    - go: foo
`)
				})

				It("does not parse", func() {
					Expect(err).To(HaveOccurred())
				})

				It("has the expected error", func() {
					Expect(err).To(MatchError("unable to convert '<nil>' as 'value' value of a Type step"))
				})

				It("has no tabs", func() {
					Expect(tabs).To(BeEmpty())
				})
			})

			Context("empty value in value", func() {
				BeforeEach(func() {
					scrpt = []byte(`
- name: Empty Value
  script:
    - type:
        xpath: not empty
        value: ""
    - go: foo
`)
				})

				It("does not parse", func() {
					Expect(err).To(HaveOccurred())
				})

				It("has the expected error", func() {
					Expect(err).To(MatchError("either value or secret must be provided and not be empty"))
				})

				It("has no tabs", func() {
					Expect(tabs).To(BeEmpty())
				})
			})

			Context("missing value in secret", func() {
				BeforeEach(func() {
					scrpt = []byte(`
- name: Empty Value
  script:
    - type:
        xpath: not empty
        secret:
    - go: foo
`)
				})

				It("does not parse", func() {
					Expect(err).To(HaveOccurred())
				})

				It("has the expected error", func() {
					Expect(err).To(MatchError("unable to convert '<nil>' as 'secret' value of a Type step"))
				})

				It("has no tabs", func() {
					Expect(tabs).To(BeEmpty())
				})
			})

			Context("empty value in secret", func() {
				BeforeEach(func() {
					scrpt = []byte(`
- name: Empty Value
  script:
    - type:
        xpath: not empty
        secret: ""
    - go: foo
`)
				})

				It("does not parse", func() {
					Expect(err).To(HaveOccurred())
				})

				It("has the expected error", func() {
					Expect(err).To(MatchError("either value or secret must be provided and not be empty"))
				})

				It("has no tabs", func() {
					Expect(tabs).To(BeEmpty())
				})
			})
		})

		Context("Click", func() {
			Context("missing value", func() {
				BeforeEach(func() {
					scrpt = []byte(`
- name: Missing Value
  script:
    - click:
    - go: foo
`)
				})

				It("does not parse", func() {
					Expect(err).To(HaveOccurred())
				})

				It("has the expected error", func() {
					Expect(err).To(MatchError("unable to parse '<nil>' as value of a Click step"))
				})

				It("has no tabs", func() {
					Expect(tabs).To(BeEmpty())
				})
			})

			Context("empty value", func() {
				BeforeEach(func() {
					scrpt = []byte(`
- name: Empty Value
  script:
    - click: ""
    - go: foo
`)
				})

				It("does not parse", func() {
					Expect(err).To(HaveOccurred())
				})

				It("has the expected error", func() {
					Expect(err).To(MatchError("value must not be empty"))
				})

				It("has no tabs", func() {
					Expect(tabs).To(BeEmpty())
				})
			})
		})
	})

	Context("valid script", func() {
		BeforeEach(func() {
			scrpt = []byte(`
- name: Without script
- name: Empty script
  script:
- name: Hello
  script:
    - go: https://example.com
    - wait: something
    - type:
        xpath: foo
        value: bar
    - type:
        xpath: baz
        secret: s3cret
    - click: button
`)
		})

		It("parses", func() {
			Expect(err).ToNot(HaveOccurred())
		})

		It("has tabs", func() {
			Expect(tabs).ToNot(BeEmpty())
		})

		It("has the expected number of tabs", func() {
			Expect(tabs).To(HaveLen(3))
		})

		Context("tab", func() {
			var tab *script.Tab

			Context("#0", func() {
				JustBeforeEach(func() {
					tab = tabs[0]
				})

				It("has the name", func() {
					Expect(tab.Name).To(Equal("Without script"))
				})

				It("has the expected number of steps", func() {
					Expect(tab.Steps).To(HaveLen(0))
				})
			})

			Context("#1", func() {
				JustBeforeEach(func() {
					tab = tabs[1]
				})

				It("has the name", func() {
					Expect(tab.Name).To(Equal("Empty script"))
				})

				It("has the expected number of steps", func() {
					Expect(tab.Steps).To(HaveLen(0))
				})
			})

			Context("#2", func() {
				JustBeforeEach(func() {
					tab = tabs[2]
				})

				It("has the name", func() {
					Expect(tab.Name).To(Equal("Hello"))
				})

				It("has the expected number of steps", func() {
					Expect(tab.Steps).To(HaveLen(5))
				})

				Context("checking step", func() {
					var step script.Step

					Context("#0", func() {
						JustBeforeEach(func() {
							step = tab.Steps[0]
						})

						It("presents itself as expected", func() {
							Expect(step.String()).To(Equal("go to https://example.com"))
						})
					})

					Context("#1", func() {
						JustBeforeEach(func() {
							step = tab.Steps[1]
						})

						It("presents itself as expected", func() {
							Expect(step.String()).To(Equal("wait for the element addressed by 'something'"))
						})
					})

					Context("#2", func() {
						JustBeforeEach(func() {
							step = tab.Steps[2]
						})

						It("presents itself as expected", func() {
							Expect(step.String()).To(Equal("type 'bar' into the element addressed by 'foo'"))
						})
					})

					Context("#3", func() {
						JustBeforeEach(func() {
							step = tab.Steps[3]
						})

						It("presents itself as expected", func() {
							Expect(step.String()).To(Equal("type the secret into the element addressed by 'baz'"))
						})
					})

					Context("#4", func() {
						JustBeforeEach(func() {
							step = tab.Steps[4]
						})

						It("presents itself as expected", func() {
							Expect(step.String()).To(Equal("click the element addressed by 'button'"))
						})
					})
				})
			})
		})
	})
})
