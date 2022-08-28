package script_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestScript(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Script Suite")
}
