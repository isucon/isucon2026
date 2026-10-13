packer {
  required_version = "= 1.15.4"
  required_plugins {
    amazon = {
      source  = "github.com/hashicorp/amazon"
      version = "= 1.8.1"
    }
  }
}

variable "aws_region" {
  type    = string
  default = "ap-northeast-1"
}

variable "subnet_id" {
  type    = string
  default = ""
}

variable "vpc_id" {
  type    = string
  default = ""
}

locals {
  timestamp = formatdate("YYYYMMDD-hhmm", timestamp())
}

source "amazon-ebs" "ubuntu" {
  region                                    = var.aws_region
  ssh_username                              = "ubuntu"
  temporary_key_pair_type                   = "ed25519"
  temporary_security_group_source_public_ip = true
  associate_public_ip_address               = true
  subnet_id                                 = var.subnet_id != "" ? var.subnet_id : null
  vpc_id                                    = var.vpc_id != "" ? var.vpc_id : null

  source_ami_filter {
    filters = {
      name                = "ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-amd64-server-*"
      root-device-type    = "ebs"
      virtualization-type = "hvm"
    }
    most_recent = true
    owners      = ["099720109477"]
  }
}

build {
  name = "envcheck"

  source "amazon-ebs.ubuntu" {
    name          = "envcheck"
    ami_name      = "isucon2026-envcheck-${local.timestamp}"
    instance_type = "c7i.large"
    tags          = { Project = "isucon2026", Role = "envcheck" }

    launch_block_device_mappings {
      device_name           = "/dev/sda1"
      volume_size           = 8
      volume_type           = "gp3"
      delete_on_termination = true
    }
  }

  provisioner "file" {
    source      = "${path.root}/../../../portal/bin/isucon-env-checker"
    destination = "/tmp/isucon-env-checker"
  }

  provisioner "file" {
    source      = "${path.root}/isucon-env-checker.service"
    destination = "/tmp/isucon-env-checker.service"
  }

  provisioner "shell" {
    script = "${path.root}/install.sh"
  }
}
