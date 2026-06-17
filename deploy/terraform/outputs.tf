output "cluster_id" {
  value = digitalocean_kubernetes_cluster.viro.id
}

output "cluster_endpoint" {
  value     = digitalocean_kubernetes_cluster.viro.endpoint
  sensitive = true
}

output "registry_endpoint" {
  value = digitalocean_container_registry.viro.endpoint
}

output "postgres_uri" {
  value     = var.create_managed_postgres ? digitalocean_database_cluster.viro_pg[0].uri : ""
  sensitive = true
}

output "kubeconfig_command" {
  value = "doctl kubernetes cluster kubeconfig save ${digitalocean_kubernetes_cluster.viro.name}"
}
