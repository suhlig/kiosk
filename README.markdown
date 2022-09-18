# Kiosk

Turns a Raspberry Pi into a simple browser kiosk. A Go program controls a full-screen Chromium browser.

# Preparation

1. Configure the OS image using the [Raspberry Pi Imager](https://www.raspberrypi.com/software/):

   - Set hostname
   - Enable SSH
   - Configure a non-default user
   - Configure WLAN
   - Set the locale

1. After first boot, use `raspi-config` to:

   - Auto-boot into the graphical environment without asking for login credentials
   - Disable screen blanking

# Kiosk Controller

The official touch screen makes a great secondary display for the `kiosk-controller`. Add or change the following lines in `/boot/config.txt`:

* Rotate the touchscreen by 180°:

  ```
  lcd_rotate=2
  ```

* Switch to FKMS mode:

  ```
  dtoverlay=vc4-fkms-v3d
  ```

  Make sure it's `fkms`, and not `kms`.

# Config File

The URL of the browser is described in a YAML file that is passed as argument or via `STDIN` to the `kiosk` binary. If multiple entries are present, they will be opened as browser tabs and switched between every `--interval`; e.g. `10s`.

Example:

```yaml
- name: org
  script:
    - go: https://example.org
    - click: //p/a
- name: com
  script:
    - go: https://example.com
- name: net
  script:
    - go: https://example.net
```

# TODO

- stream image updates (saves us from reloading the page)
  - or find another way to keep updating screenshots while switching is paused
- add backlight controls to HTTP control server as POST
- try using [staticClick](https://flickity.metafizzy.co/events.html#staticclick) to POST via [Fetch API](https://attacomsian.com/blog/xhr-post-request) instead of form POST (saves a page reload and should prevent flicker)
  - might also make the current page the one shown when loading the controller? If not, implement separately.
- add another service to run the kiosk controller on the touch screen:

  ```command
  $ chromium-browser --kiosk http://localhost:8011
  ```
- re-configure tab switching time in the controller
- deploy using pipeline
- test pages for presence of some element, otherwise close tab and restart (e.g. when authenticated session expires)
- refresh pages that are not self-refreshing (e.g. [reload](https://github.com/chromedp/chromedp/blob/a3b306adf4a8348197a7927cacf3e77077121dd5/nav.go#L89))
  - also useful as HTTP command
- configure `lcd_rotate=2` and `dtoverlay=vc4-fkms-v3d` via Ansible
- `unclutter -idle 0.5 -root &` if needed
- [splash screen at boot](https://github.com/guysoft/FullPageOS/blob/master/src/modules/fullpageos/filesystem/root_init/etc/systemd/system/splashscreen.service)
- Turn displays on and off via HTTP or cron:

  ```command
  # Touch Screen (primary monitor)
  $ vcgencmd display_power 0 0
  $ vcgencmd display_power 1 0

  # HDMI
  $ vcgencmd display_power 0 2
  $ vcgencmd display_power 1 2
  ```

  Alternatively:

  * Touchscreen

    ```command
    $ sudo zsh -c "echo 1 > /sys/class/backlight/rpi_backlight/bl_power" # off
    $ sudo zsh -c "echo 0 > /sys/class/backlight/rpi_backlight/bl_power" # on
    ```


  * HDMI (`2` is the HDMI port; use `tvservice --list` to list)

    ```command
    $ tvservice -o -v 2
    $ tvservice -p -v 0
    ```

# Deployment

## Build the `kiosk` binary

For the Raspberry Pi 4, this is

```command
$ GOARM=7 GOARCH=arm GOOS=linux go build
```

## Prerequisites on the Deployer's Workstation

* Make sure you have a recent [Ansible installation](http://docs.ansible.com/ansible/intro_installation.html):

  ```bash
  $ brew install ansible
  ```

  Replace `brew` with `yum` or `apt-get`, depending on your OS.

* Install the required Ansible roles:

  ```bash
  $ ansible-galaxy install -r roles.yml
  ```

* Update `inventory.yml` with the host(s) to deploy to

## Deploy

Run the playbook:

```bash
$ ansible-playbook playbook.yml
```
