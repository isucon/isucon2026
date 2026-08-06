build {
  name    = "problem-server-amd64"
  sources = ["source.vagrant.problem_server"]

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
}

build {
  name    = "router-amd64"
  sources = ["source.vagrant.router"]

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
}
