# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 Dave O'Connor
#
# andante on Nomad. See docs/deploying.md.
#
# Postgres must already exist, with a database for each service. The DSNs and
# tokens come from a Nomad Variable, created once:
#
#   nomad var put nomad/jobs/andante \
#     database_url='postgres://andante:...@db.example:5432/andante' \
#     catalogue_database_url='postgres://catalogue:...@db.example:5432/catalogue' \
#     secret='...' \
#     catalogue_token='...'
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
      port "ops" {
        to = 8081
      }
      port "catalogue" {
        to = 8082
      }
    }

    service {
      name     = "andante"
      provider = "nomad"
      port     = "public"
    }

    service {
      name     = "andante-ops"
      provider = "nomad"
      port     = "ops"

      check {
        name     = "ready"
        type     = "http"
        path     = "/readyz"
        interval = "15s"
        timeout  = "5s"
      }
    }

    service {
      name     = "andante-catalogue"
      provider = "nomad"
      port     = "catalogue"

      check {
        name     = "ready"
        type     = "http"
        path     = "/readyz"
        interval = "15s"
        timeout  = "5s"
      }
    }

    # Both tasks are in one network, so the server reaches this on loopback. The port
    # is there for Prometheus.
    task "catalogue" {
      driver = "docker"

      config {
        image      = "ghcr.io/gerrowadat/andante:0.0.1"
        entrypoint = ["/usr/local/bin/andante-catalogue"]
        ports      = ["catalogue"]
      }

      env {
        ANDANTE_BASE_URL         = "https://feeds.example.com"
        ANDANTE_CATALOGUE_LISTEN = ":8082"
      }

      template {
        destination = "secrets/catalogue.env"
        env         = true
        data        = <<-EOT
          {{ with nomadVar "nomad/jobs/andante" -}}
          ANDANTE_CATALOGUE_DATABASE_URL={{ .catalogue_database_url }}
          ANDANTE_CATALOGUE_TOKEN={{ .catalogue_token }}
          {{- end }}
        EOT
      }

      resources {
        cpu    = 200
        memory = 128
      }
    }

    task "server" {
      driver = "docker"

      config {
        image = "ghcr.io/gerrowadat/andante:0.0.1"
        ports = ["public", "ops"]
      }

      env {
        ANDANTE_BASE_URL      = "https://feeds.example.com"
        ANDANTE_LISTEN        = ":8080"
        ANDANTE_OPS_LISTEN    = ":8081"
        ANDANTE_CATALOGUE_URL = "http://127.0.0.1:8082/v1"
        # The proxy that terminates TLS, so that logins are throttled by the real address.
        # ANDANTE_TRUSTED_PROXIES = "10.0.0.0/8"
        # Log in with an OpenID provider. Its client secret goes in the template below.
        # ANDANTE_OIDC_ISSUER         = "https://id.example.com"
        # ANDANTE_OIDC_CLIENT_ID      = "andante"
        # ANDANTE_OIDC_ALLOWED_DOMAIN = "example.com"
      }

      template {
        destination = "secrets/andante.env"
        env         = true
        data        = <<-EOT
          {{ with nomadVar "nomad/jobs/andante" -}}
          ANDANTE_DATABASE_URL={{ .database_url }}
          ANDANTE_SECRET={{ .secret }}
          ANDANTE_CATALOGUE_TOKEN={{ .catalogue_token }}
          {{ with .oidc_client_secret }}ANDANTE_OIDC_CLIENT_SECRET={{ . }}{{ end }}
          {{- end }}
        EOT
      }

      resources {
        cpu    = 200
        memory = 256
      }
    }
  }
}
