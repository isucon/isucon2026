packer {
  required_plugins {
    vagrant = {
      source  = "github.com/hashicorp/vagrant"
      version = "= 1.1.7"
    }
    virtualbox = {
      source  = "github.com/hashicorp/virtualbox"
      version = "= 1.1.5"
    }
  }
}

variable "architecture" {
  type    = string
  default = "amd64"

  validation {
    condition     = var.architecture == "amd64"
    error_message = "Architecture must be amd64."
  }
}

variable "release_id" {
  type    = string
  default = "r2026.08.06.3"
}

variable "ubuntu_iso_url" {
  type    = string
  default = "https://releases.ubuntu.com/noble/ubuntu-24.04.4-live-server-amd64.iso"
}

variable "ubuntu_iso_checksum" {
  type    = string
  default = "sha256:e907d92eeec9df64163a7e454cbc8d7755e8ddc7ed42f99dbc80c40f1a138433"
}

source "virtualbox-iso" "ubuntu_noble_amd64" {
  vm_name              = "isureal-ubuntu-24.04.4-amd64-${var.release_id}"
  guest_os_type        = "Ubuntu_64"
  firmware             = "efi"
  chipset              = "ich9"
  cpus                 = 4
  memory               = 4096
  disk_size            = 24576
  hard_drive_interface = "sata"
  iso_interface        = "sata"
  iso_url              = var.ubuntu_iso_url
  iso_checksum         = var.ubuntu_iso_checksum
  headless             = true
  audio_controller     = "none"
  rtc_time_base        = "UTC"

  http_directory = "${path.root}/http"
  boot_wait      = "5s"
  boot_command = [
    "<esc><wait>c<wait>",
    "linux /casper/vmlinuz autoinstall ds=nocloud-net\\;s=http://{{ .HTTPIP }}:{{ .HTTPPort }}/ ---<enter><wait>",
    "initrd /casper/initrd<enter><wait>",
    "boot<enter>"
  ]

  communicator = "ssh"
  ssh_username = "vagrant"
  ssh_password = "vagrant"
  ssh_timeout  = "90m"
  # The live installer exposes ssh before the target OS exists.  Nested
  # VirtualBox can spend more than 20 minutes creating initramfs, so let the
  # timeout—not the transient authentication-attempt counter—bound the build.
  ssh_handshake_attempts = 1000
  shutdown_command       = "echo 'vagrant' | sudo -S shutdown -P now"
  shutdown_timeout       = "10m"

  guest_additions_mode = "disable"
  format               = "ova"
  output_directory     = "${path.root}/../../.state/images/${var.release_id}/base-raw"

  vboxmanage = [
    ["modifyvm", "{{.Name}}", "--nested-hw-virt", "off"],
    ["modifyvm", "{{.Name}}", "--vram", "16"],
    ["modifyvm", "{{.Name}}", "--clipboard-mode", "disabled"],
    ["modifyvm", "{{.Name}}", "--drag-and-drop", "disabled"]
  ]
}

build {
  name    = "ubuntu-noble-amd64-base"
  sources = ["source.virtualbox-iso.ubuntu_noble_amd64"]

  provisioner "shell" {
    execute_command = "echo 'vagrant' | {{ .Vars }} sudo -S -E bash '{{ .Path }}'"
    inline = [
      "cloud-init status --wait",
      "install -d -m 0700 -o vagrant -g vagrant /home/vagrant/.ssh",
      "chmod 0600 /home/vagrant/.ssh/authorized_keys",
      "echo 'vagrant ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/99-vagrant",
      "chmod 0440 /etc/sudoers.d/99-vagrant",
      "sync"
    ]
  }

  post-processors {
    post-processor "vagrant" {
      architecture        = "amd64"
      compression_level   = 6
      keep_input_artifact = false
      output              = "${path.root}/../../.state/images/${var.release_id}/ubuntu-24.04.4-amd64-virtualbox.box"
    }
    post-processor "manifest" {
      output     = "${path.root}/../../.state/images/${var.release_id}/base-packer-manifest.json"
      strip_path = false
      custom_data = {
        architecture = "amd64"
        release_id   = var.release_id
        role         = "base"
      }
    }
  }
}
