terraform {
  required_version = ">= 1.6.0"
  required_providers {
    digitalocean = {
      source  = "digitalocean/digitalocean"
      version = "~> 2.43"
    }
  }
}

provider "digitalocean" {
  token = var.do_token
}

# Container registry for the Viro images.
resource "digitalocean_container_registry" "viro" {
  name                   = var.registry_name
  subscription_tier_slug = "basic"
  region                 = var.region
}

# VPC for the cluster.
resource "digitalocean_vpc" "viro" {
  name   = "${var.cluster_name}-vpc"
  region = var.region
}

# DOKS cluster with an autoscaling node pool.
resource "digitalocean_kubernetes_cluster" "viro" {
  name     = var.cluster_name
  region   = var.region
  version  = var.k8s_version
  vpc_uuid = digitalocean_vpc.viro.id

  node_pool {
    name       = "default"
    size       = var.node_size
    auto_scale = true
    min_nodes  = var.min_nodes
    max_nodes  = var.max_nodes
  }
}

# Managed Postgres for the control-plane (production-grade, instead of in-cluster).
resource "digitalocean_database_cluster" "viro_pg" {
  count      = var.create_managed_postgres ? 1 : 0
  name       = "${var.cluster_name}-pg"
  engine     = "pg"
  version    = "16"
  size       = var.pg_size
  region     = var.region
  node_count = 1
  private_network_uuid = digitalocean_vpc.viro.id
}
