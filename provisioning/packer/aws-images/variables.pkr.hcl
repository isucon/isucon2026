variable "architecture" {
  type        = string
  default     = "amd64"
  description = "Only amd64 is supported."

  validation {
    condition     = var.architecture == "amd64"
    error_message = "Architecture must be amd64."
  }
}

variable "aws_region" {
  type = string
}

variable "source_ami_id" {
  type = string
}

variable "source_ami_owner_id" {
  type = string
}

variable "release_id" {
  type = string
}

variable "release_manifest_path" {
  type = string
}

variable "artifact_root" {
  type = string
}

variable "build_instance_type" {
  type    = string
  default = "t3.medium"
}

variable "subnet_id" {
  type        = string
  default     = ""
  description = "Optional public subnet used by the temporary builder instance."
}

variable "vpc_id" {
  type        = string
  default     = ""
  description = "Optional VPC used by the temporary builder instance."
}

variable "manifest_path" {
  type = string
}
