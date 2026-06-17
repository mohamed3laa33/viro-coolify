variable "do_token" {
  description = "DigitalOcean API token"
  type        = string
  sensitive   = true
}

variable "cluster_name" {
  type    = string
  default = "viro"
}

variable "registry_name" {
  type    = string
  default = "viro"
}

variable "region" {
  type    = string
  default = "fra1"
}

variable "k8s_version" {
  description = "DOKS version slug (run: doctl kubernetes options versions)"
  type        = string
  default     = "1.31.1-do.0"
}

variable "node_size" {
  type    = string
  default = "s-2vcpu-4gb"
}

variable "min_nodes" {
  type    = number
  default = 2
}

variable "max_nodes" {
  type    = number
  default = 5
}

variable "create_managed_postgres" {
  type    = bool
  default = true
}

variable "pg_size" {
  type    = string
  default = "db-s-1vcpu-1gb"
}
