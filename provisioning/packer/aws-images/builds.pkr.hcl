build {
  name    = "problem-server-amd64"
  sources = ["source.amazon-ebs.problem_server"]

  provisioner "ansible" {
    playbook_file = "${path.root}/../../ansible/playbooks/image.yml"
    extra_arguments = [
      "--extra-vars", "image_role=problem-server",
    ]
    ansible_env_vars = [
      "ANSIBLE_CONFIG=${path.root}/../../ansible/ansible.cfg",
      "VM_RELEASE=${var.release_manifest_path}",
      "ISUREAL_ARTIFACT_ROOT=${var.artifact_root}",
    ]
  }

  post-processor "manifest" {
    custom_data = {
      architecture    = var.architecture
      release_id      = var.release_id
      role            = "problem-server"
      source_ami_id   = var.source_ami_id
      source_owner_id = var.source_ami_owner_id
    }
    output     = var.manifest_path
    strip_path = true
  }
}

build {
  name    = "router-amd64"
  sources = ["source.amazon-ebs.router"]

  provisioner "ansible" {
    playbook_file = "${path.root}/../../ansible/playbooks/image.yml"
    extra_arguments = [
      "--extra-vars", "image_role=router",
    ]
    ansible_env_vars = [
      "ANSIBLE_CONFIG=${path.root}/../../ansible/ansible.cfg",
      "VM_RELEASE=${var.release_manifest_path}",
      "ISUREAL_ARTIFACT_ROOT=${var.artifact_root}",
    ]
  }

  post-processor "manifest" {
    custom_data = {
      architecture    = var.architecture
      release_id      = var.release_id
      role            = "router"
      source_ami_id   = var.source_ami_id
      source_owner_id = var.source_ami_owner_id
    }
    output     = var.manifest_path
    strip_path = true
  }
}
