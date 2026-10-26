variable "aws_region" {
  type    = string
  default = "ap-northeast-1"
}

variable "subnet_id" {
  type        = string
  default     = ""
  description = "Public subnet for the temporary build instance. The default VPC is used when empty."
}

variable "vpc_id" {
  type    = string
  default = ""
}
