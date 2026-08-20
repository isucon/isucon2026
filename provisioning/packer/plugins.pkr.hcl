packer {
  required_plugins {
    amazon = {
      source  = "github.com/hashicorp/amazon"
      version = "= 1.8.1"
    }
    ansible = {
      source  = "github.com/hashicorp/ansible"
      version = "= 1.1.5"
    }
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
