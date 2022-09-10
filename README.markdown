# Kiosk

Turns a Raspberry Pi into a simple browser kiosk. A Go program controls a full-screen Chromium browser.

Configuring the Pi is described in the `nerab.raspi` role. Make sure the Pi auto-boots into the graphical environment without asking for login credentials (see `raspi-config`).

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

The `kiosk` program accepts commands like `pause`, `resume`, `next`, `previous` via MQTT.

A gallery of tabs is presented via HTTP.

# TODO

- resize images to something _much_ smaller (e.g. [in pure Go](https://gist.github.com/logrusorgru/570d64fd6a051e0441014387b89286ca))
- test pages for presence of some element, otherwise close tab and restart (e.g. when authenticated session expires)
- refresh pages that are not self-refreshing (browser-reload or recreate the tab)
- `unclutter -idle 0.5 -root &` if needed
- [splash screen at boot](https://github.com/guysoft/FullPageOS/blob/master/src/modules/fullpageos/filesystem/root_init/etc/systemd/system/splashscreen.service)

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
