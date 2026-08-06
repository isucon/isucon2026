packer {
  required_plugins {
    ansible = {
      source  = "github.com/hashicorp/ansible"
      version = "= 1.1.5"
    }
    vagrant = {
      source  = "github.com/hashicorp/vagrant"
      version = "= 1.1.7"
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

variable "base_box_path" {
  type    = string
  default = ""
}

variable "release_manifest_path" {
  type    = string
  default = ""
}

variable "artifact_root" {
  type    = string
  default = ""
}

locals {
  resolved_base_box = var.base_box_path != "" ? var.base_box_path : "${path.root}/../../.state/images/${var.release_id}/ubuntu-24.04.4-amd64-virtualbox.box"
}

source "vagrant" "problem_server" {
  communicator = "ssh"
  source_path  = local.resolved_base_box
  box_name     = "isureal/base-${var.release_id}"
  provider     = "virtualbox"
  add_force    = true
  insert_key   = false
  template     = "${path.root}/Vagrantfile.tpl"
  output_dir   = "${path.root}/../../.state/images/${var.release_id}/problem-server-output"
}

source "vagrant" "router" {
  communicator = "ssh"
  source_path  = local.resolved_base_box
  box_name     = "isureal/base-${var.release_id}"
  provider     = "virtualbox"
  add_force    = true
  insert_key   = false
  template     = "${path.root}/Vagrantfile.tpl"
  output_dir   = "${path.root}/../../.state/images/${var.release_id}/router-output"
}
