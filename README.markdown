# Kiosk

Turns a Raspberry Pi into a simple browser kiosk

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

* Update `hosts` with the IP address of the host(s) to deploy to. The deploy is more convenient if you can ssh into them without having to enter a password.

## Prerequisites on the Raspi

* Configure some generic settings using `sudo raspi-config`:
  - Change password of user `pi`
  - Set the hostname (`kiosk`)
  - Enable SSH server
  - Expand root filesystem
* Connect via Ethernet
* Copy the public SSH key to the pi with `ssh-copy-id pi@kiosk`.

## Deploy

Run the playbook:

```bash
$ ansible-playbook -i hosts site.yml
```

## WiFi

If you want to configure the WiFi network, create a file `wifi.yml` with the following contents (adapt it to your WIFI settings):

```yaml
wlan_country: DE
wlan_ssid: your-wlan-ssid
wlan_password: your-wlan-password
```

If `wifi.yml` is not present, WiFi will not be configured.
