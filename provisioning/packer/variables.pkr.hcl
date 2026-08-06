variable "architecture" {
  type        = string
  default     = "amd64"
  description = "Only amd64 is supported by this feature."

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
