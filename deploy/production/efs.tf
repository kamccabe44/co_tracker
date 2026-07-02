# Fargate tasks have no persistent local disk, so the SQLite file has to live
# somewhere that survives task restarts and redeploys. This is what keeps the
# whole app (including its data) one deployable unit — a single container, no
# separate database service to run, back up, or pay for on its own.
resource "aws_efs_file_system" "data" {
  creation_token  = "${local.name}-data"
  encrypted       = true
  throughput_mode = "bursting"

  lifecycle_policy {
    transition_to_ia = "AFTER_30_DAYS"
  }

  tags = { Name = "${local.name}-data" }
}

resource "aws_efs_mount_target" "data" {
  count           = var.az_count
  file_system_id  = aws_efs_file_system.data.id
  subnet_id       = aws_subnet.app[count.index].id
  security_groups = [aws_security_group.efs.id]
}

# Pins the mount to one subdirectory and to the same non-root UID/GID (65532)
# the container already runs as (see Dockerfile), so files land with the
# right ownership with no root-squash workarounds.
resource "aws_efs_access_point" "data" {
  file_system_id = aws_efs_file_system.data.id

  posix_user {
    uid = 65532
    gid = 65532
  }

  root_directory {
    path = "/co_tracker"
    creation_info {
      owner_uid   = 65532
      owner_gid   = 65532
      permissions = "0755"
    }
  }

  tags = { Name = "${local.name}-data-ap" }
}
