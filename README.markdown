# Kiosk

Turns a Raspberry Pi into a simple browser kiosk. A Go program controls a full-screen Chromium browser.

Configuring the Pi is described in the `nerab.raspi` role. Make sure the Pi auto-boots into the graphical environment without asking for login credentials (see `raspi-config`).

# TODO

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
