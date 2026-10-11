# Example only: review before running. Creates the first M2M credential for the
# Logto Management API (idempotent, safe to re-run).
#
# Assumptions to check against the cluster:
#   - Logto's Postgres URL is in Vault at secret/default/logto/db, key DB_URL.
#   - The M2M credential lives at secret/default/logto/m2m with keys
#     LOGTO_APP_ID and LOGTO_APP_SECRET. Create it once, e.g.:
#       logto-cli bootstrap --generate --db-url ... (prints both lines), then
#       vault kv put secret/default/logto/m2m LOGTO_APP_ID=... LOGTO_APP_SECRET=...
#     or generate the values yourself (ID: <=21 chars [a-z0-9], secret: <=64 chars).
#   - Logto core is reachable at LOGTO_ENDPOINT below (used to verify the credential).
#   - The image is pinned by the digest printed in the release workflow summary.

job "logto-bootstrap" {
  namespace = "default"
  type      = "batch"

  group "bootstrap" {
    count = 1

    # Logto seeds its database on start; if this runs first, bootstrap exits 3
    # ("Logto not seeded yet?") and Nomad retries.
    restart {
      attempts = 5
      delay    = "30s"
      mode     = "fail"
    }

    reschedule {
      attempts  = 3
      interval  = "1h"
      delay     = "1m"
      unlimited = false
    }

    task "bootstrap" {
      driver = "docker"

      config {
        image = "ghcr.io/reapermaga/logto-cli@sha256:REPLACE_WITH_RELEASE_DIGEST"
        args  = ["bootstrap"]
      }

      vault {}

      template {
        destination = "secrets/logto.env"
        env         = true
        change_mode = "noop"
        data        = <<-EOT
          {{ with secret "secret/data/default/logto/db" }}
          LOGTO_DB_URL={{ .Data.data.DB_URL }}
          {{ end }}
          {{ with secret "secret/data/default/logto/m2m" }}
          LOGTO_APP_ID={{ .Data.data.LOGTO_APP_ID }}
          LOGTO_APP_SECRET={{ .Data.data.LOGTO_APP_SECRET }}
          {{ end }}
        EOT
      }

      env {
        # Optional: verify the credential against the API after bootstrapping.
        LOGTO_ENDPOINT = "https://auth.example.com"
      }

      resources {
        cpu    = 100
        memory = 64
      }
    }
  }
}
