# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 Dave O'Connor
#
# andante on Nomad. See docs/deploying.md.
#
# Postgres must already exist. The DSN and bootstrap token come from a Nomad
# Variable, created once:
#
#   nomad var put nomad/jobs/andante \
#     database_url='postgres://andante:...@db.example:5432/andante' \
#     admin_token='...'
#
# Change datacenters and ANDANTE_BASE_URL, and pin the image to a release.

job "andante" {
  datacenters = ["dc1"]
  type        = "service"

  # Opts in to drift detection by nomad-gitops.
  meta {
    gitops_managed = "true"
  }

  group "andante" {
    # One replica is the supported deployment. A second would serve feeds and not
    # crawl, because the crawl loop takes a Postgres advisory lock.
    count = 1

    network {
      mode = "bridge"

      port "public" {
        to = 8080
      }
      port "admin" {
        to = 8081
      }
    }

    service {
      name     = "andante"
      provider = "nomad"
      port     = "public"
    }

    service {
      name     = "andante-admin"
      provider = "nomad"
      port     = "admin"

      check {
        name     = "ready"
        type     = "http"
        path     = "/readyz"
        interval = "15s"
        timeout  = "5s"
      }
    }

    task "server" {
      driver = "docker"

      config {
        image = "ghcr.io/gerrowadat/andante:0.1.0"
        ports = ["public", "admin"]
      }

      env {
        ANDANTE_BASE_URL     = "https://feeds.example.com"
        ANDANTE_LISTEN       = ":8080"
        ANDANTE_ADMIN_LISTEN = ":8081"
      }

      template {
        destination = "secrets/andante.env"
        env         = true
        data        = <<-EOT
          {{ with nomadVar "nomad/jobs/andante" -}}
          ANDANTE_DATABASE_URL={{ .database_url }}
          ANDANTE_ADMIN_TOKEN={{ .admin_token }}
          ANDANTE_TOKEN={{ .admin_token }}
          {{- end }}
        EOT
      }

      resources {
        cpu    = 200
        memory = 128
      }
    }
  }
}
