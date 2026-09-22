locals {
  common_tags = {
    "Project"      = "isucon2026"
    "ReleaseId"    = var.release_id
    "Architecture" = var.architecture
    "ManagedBy"    = "packer"
  }
}

source "amazon-ebs" "problem_server" {
  ami_name                                  = "isucon2026-${var.release_id}-problem-server-amd64"
  ami_description                           = "ISUCON 2026 ${var.release_id} problem server"
  associate_public_ip_address               = true
  ena_support                               = true
  instance_type                             = var.build_instance_type
  region                                    = var.aws_region
  source_ami                                = var.source_ami_id
  ssh_username                              = "ubuntu"
  subnet_id                                 = var.subnet_id != "" ? var.subnet_id : null
  temporary_security_group_source_public_ip = true
  vpc_id                                    = var.vpc_id != "" ? var.vpc_id : null

  launch_block_device_mappings {
    device_name           = "/dev/sda1"
    delete_on_termination = true
    encrypted             = true
    volume_size           = 40
    volume_type           = "gp3"
  }

  tags = merge(local.common_tags, {
    "Name" = "isucon2026-${var.release_id}-problem-server-amd64"
    "Role" = "problem-server"
  })

  run_tags = merge(local.common_tags, {
    "Name" = "Packer ISUCON 2026 problem-server"
    "Role" = "problem-server"
  })
}

source "amazon-ebs" "router" {
  ami_name                                  = "isucon2026-${var.release_id}-router-amd64"
  ami_description                           = "ISUCON 2026 ${var.release_id} Router"
  associate_public_ip_address               = true
  ena_support                               = true
  instance_type                             = var.build_instance_type
  region                                    = var.aws_region
  source_ami                                = var.source_ami_id
  ssh_username                              = "ubuntu"
  subnet_id                                 = var.subnet_id != "" ? var.subnet_id : null
  temporary_security_group_source_public_ip = true
  vpc_id                                    = var.vpc_id != "" ? var.vpc_id : null

  launch_block_device_mappings {
    device_name           = "/dev/sda1"
    delete_on_termination = true
    encrypted             = true
    volume_size           = 20
    volume_type           = "gp3"
  }

  tags = merge(local.common_tags, {
    "Name" = "isucon2026-${var.release_id}-router-amd64"
    "Role" = "router"
  })

  run_tags = merge(local.common_tags, {
    "Name" = "Packer ISUCON 2026 router"
    "Role" = "router"
  })
}
