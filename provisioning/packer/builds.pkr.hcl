build {
  source "amazon-ebs.ubuntu" {
    name          = "problem-server"
    ami_name      = "isucon2026-problem-server-${local.timestamp}"
    instance_type = "c6i.2xlarge"
    tags          = { Project = "isucon2026", Role = "problem-server" }

    # 初期データは画像だけで十数 GB ある。インスタンスのディスクもこの大きさになる
    launch_block_device_mappings {
      device_name           = "/dev/sda1"
      volume_size           = 60
      volume_type           = "gp3"
      delete_on_termination = true
    }
  }

  source "amazon-ebs.ubuntu" {
    name          = "router"
    ami_name      = "isucon2026-router-${local.timestamp}"
    instance_type = "c6i.xlarge"
    tags          = { Project = "isucon2026", Role = "router" }

    launch_block_device_mappings {
      device_name           = "/dev/sda1"
      volume_size           = 20
      volume_type           = "gp3"
      delete_on_termination = true
    }
  }

  provisioner "ansible" {
    only             = ["amazon-ebs.problem-server"]
    playbook_file    = "${path.root}/../ansible/problem-server.yml"
    groups           = ["problem_servers"]
    use_proxy        = false
    ansible_env_vars = ["ANSIBLE_CONFIG=${path.root}/../ansible/ansible.cfg"]
  }

  provisioner "ansible" {
    only             = ["amazon-ebs.router"]
    playbook_file    = "${path.root}/../ansible/router.yml"
    groups           = ["routers"]
    use_proxy        = false
    ansible_env_vars = ["ANSIBLE_CONFIG=${path.root}/../ansible/ansible.cfg"]
  }

  provisioner "shell" {
    inline = [
      "sudo cloud-init clean --logs",
      "sudo truncate -s 0 /etc/machine-id /home/ubuntu/.ssh/authorized_keys",
    ]
  }
}
