log_level = "TRACE"

client {
  cni_config_dir = "/etc/cni/conf.d"
}

plugin "firecracker" {
  config {
    firecracker_bin_path = "/usr/bin/firecracker"
    firecracker_data_path = "/run/firecracker"
    
    cni_config_path = "/etc/cni/conf.d"
    cni_bin_path = "/opt/cni/bin"
    cni_data_path = "/var/lib/cni/networks"
  }
}
