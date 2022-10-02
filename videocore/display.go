package videocore

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

type DisplayStatus struct {
	ID     uint8 `json:"display"`
	Status bool  `json:"status"`
}

func (ds DisplayStatus) String() string {
	return fmt.Sprintf("%v: %v", ds.ID, ds.Status)
}

func GetDisplays() (ids []uint8, err error) {
	cmd := exec.Command("tvservice", "-l")

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	err = cmd.Run()

	if err != nil {
		var pErr *exec.ExitError

		if errors.As(err, &pErr) {
			err = fmt.Errorf("unable to retrieve displays: tvservice exited %d. Message: %s", pErr.ExitCode(), pErr.Stderr)
		} else {
			err = fmt.Errorf("unable to retrieve displays: %s", err)
		}

		return
	}

	lines := new(bytes.Buffer)
	_, err = io.Copy(lines, &stdout)

	if err != nil {
		return
	}

	scanner := bufio.NewScanner(lines)
	scanner.Split(bufio.ScanLines)

	var lineNumber int
	var displayCount int

	for scanner.Scan() {
		words := strings.Fields(scanner.Text())

		if lineNumber == 0 {
			// first line has the number of displays
			// e.g.
			// 2 attached device(s), display ID's are :
			_, err = fmt.Sscan(words[0], &displayCount)

			if err != nil {
				return
			}
		} else {
			// following lines are displays
			// e.g.
			// Display Number 0, type Main LCD
			// Display Number 2, type HDMI 0
			var id uint8
			_, err = fmt.Sscan(words[2], &id)

			if err != nil {
				return
			}

			ids = append(ids, id)
		}

		lineNumber += 1
	}

	return
}

func GetBacklight(id uint8) (status bool, err error) {
	cmd := exec.Command("vcgencmd", "display_power", "-1", fmt.Sprintf("%d", id))

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	err = cmd.Run()

	if err != nil {
		var pErr *exec.ExitError

		if errors.As(err, &pErr) {
			err = fmt.Errorf("unable to retrieve displays: tvservice exited %d. Message: %s", pErr.ExitCode(), pErr.Stderr)
		} else {
			err = fmt.Errorf("unable to retrieve displays: %s", err)
		}

		return
	}

	return parseResponse(&stdout)
}

func ToggleBacklight(id uint8) (bool, error) {
	status, err := GetBacklight(id)

	if err != nil {
		return false, err
	}

	return SetBacklight(id, !status)
}

func SetBacklight(id uint8, status bool) (newStatus bool, err error) {
	var on_off string

	if status {
		on_off = "1"
	} else {
		on_off = "0"
	}

	cmd := exec.Command("vcgencmd", "display_power", on_off, fmt.Sprintf("%d", id))

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	err = cmd.Run()

	if err != nil {
		var pErr *exec.ExitError

		if errors.As(err, &pErr) {
			err = fmt.Errorf("unable to set backlight: vcgencmd exited %d. Message: %s", pErr.ExitCode(), pErr.Stderr)
		} else {
			err = fmt.Errorf("unable to set backlight: %s", err)
		}

		return
	}

	return parseResponse(&stdout)
}

func parseResponse(stdout io.Reader) (status bool, err error) {
	lines := new(bytes.Buffer)
	_, err = io.Copy(lines, stdout)

	if err != nil {
		return
	}

	scanner := bufio.NewScanner(lines)
	scanner.Split(bufio.ScanLines)

	for scanner.Scan() {
		line := scanner.Text()

		parts := strings.Split(line, "=")

		if len(parts) != 2 {
			err = fmt.Errorf("unable to interpret %v as display status", line)
			return
		}

		if parts[0] != "display_power" {
			err = fmt.Errorf("unexpected key %v; the only acceptable value is 'display_power'", parts[0])
			return
		}

		switch parts[1] {
		case "0":
			status = false
		case "1":
			status = true
		default:
			err = fmt.Errorf("unable to interpret %v as display status; acceptable values are 0 and 1", parts[1])
			return
		}
	}

	return
}
