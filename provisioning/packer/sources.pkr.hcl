locals {
  timestamp = formatdate("YYYYMMDD-hhmm", timestamp())
}

data "amazon-ami" "ubuntu_noble" {
  filters = {
    name                = "ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-amd64-server-*"
    root-device-type    = "ebs"
    virtualization-type = "hvm"
  }
  most_recent = true
  owners      = ["099720109477"]
  region      = var.aws_region
}

source "amazon-ebs" "ubuntu" {
  region                                    = var.aws_region
  source_ami                                = data.amazon-ami.ubuntu_noble.id
  ssh_username                              = "ubuntu"
  temporary_key_pair_type                   = "ed25519"
  temporary_security_group_source_public_ip = true
  associate_public_ip_address               = true
  subnet_id                                 = var.subnet_id != "" ? var.subnet_id : null
  vpc_id                                    = var.vpc_id != "" ? var.vpc_id : null
}
