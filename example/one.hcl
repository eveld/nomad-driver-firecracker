variable "machine_path" {
  default = "/home/erik/code/hashicorp/jumppad/env/v6/machines/ubuntu"
}

job "one" {
  datacenters = ["dc1"]
  type        = "service"

  group "vm" {
    restart {
      attempts = 0
      mode     = "fail"
    }

    task "vm" {
      driver = "firecracker"

      config {
        kernel = "${var.machine_path}/vmlinux"
        kernel_args = "console=ttyS0 reboot=k panic=1 pci=off overlay_root=vdb init=/sbin/overlay-init"
        
        disk {
          root_device = true
          readonly = true
          path= "${var.machine_path}/rootfs.img"
        }
        
        disk {
          path= "${var.machine_path}/overlay-erik.ext4"
        }

        network = "firecracker"

        metadata = <<EOF
        {
          "instance-id": "one",
          "hostname": "one",
          "dns": "8.8.8.8",
          "users": [
            {
              "name": "erik",
              "shell": "/bin/bash",
              "groups": ["docker"],
              "sudo": "ALL=(ALL) NOPASSWD:ALL",
              "password": "test",
              "ssh-authorized-keys": [
                "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAACAQC1zCC4eDPTb3v8Zm/wJd0Ow7UyReehHxP0F79Q0RDM9xn+UBKss1fFjAJ4HxabVOeaW4+iel0XNNmXv/+2+pfX7hHzyoup98B+ZYcLJZR5BTWEgbDiYR20gn7772VDBZABUipeoxNq/azPtQt6IZxMtV7fADlDkQWUtq7f2BzJw3DMQFkPGH84DE4MWKHnY7JNl6r9+76omeE+Ci05PjTtcgq7wjPKl8XMoFffmYHzwBz7qlaArTCdicO28EYmEYE29L+c//Ev/aX0SVmd5I7vvkkzJup/n3cjMR74Zq9F9mht8Z96JHESmzPGnPMs/ssKO1b4IXMDJC0XnaEdZUkUoTMV3najQuHp1WmbSDxjMZy28rtBvKvy1fyfRZderpROf4YkpeWue3kJ2Fx6mN63ffnRXl4HSuJtt63M8Hre8kwQWr0oT448D2EeRLjQj+HI5U21BRlD5mfk+AzHejMIIFF8hYCsos2F9B17V26hy2BfQaHFbNR5R7j2NouBVxb2ql8yuAJigYdg2M/gm4ILZre90zTxTgQL1ELbNFTvuR2BgMeJ1DAOlWLUKuo/qyrYJ8+k66wmCzp685r4xZgdHkfjLY199VYigPJ4hr7VqxoxD7EQyvg3egJReonan1EvQHO3eP5dRGo36TCzbnIsl/dnM1ETV9ez2BvSZPMzZQ== eveld@hashicorp.com"
              ]
            }
          ]
        }
        EOF
      }

      resources {
        cpu = 1024
        memory = 1024
      }
    }
  }
}
