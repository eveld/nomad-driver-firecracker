log_level = "TRACE"

client {
  cni_config_dir = "/etc/cni/conf.d"
}

plugin "firecracker" {
  config {
    firecracker = "/usr/bin/firecracker"
    cni_config_path = "/etc/cni/conf.d"
    cni_bin_path = "/opt/cni/bin"
  }
}
