resource "terraform_data" "this" {
  input = {
    model          = var.model
    app_a_name     = var.application_a.name
    app_a_endpoint = var.application_a.endpoint
    app_b_name     = var.application_b.name
    app_b_endpoint = var.application_b.endpoint
    wait_seconds   = var.wait_seconds
  }

  provisioner "local-exec" {
    interpreter = ["/bin/bash", "-c"]
    environment = {
      JUJU_MODEL     = self.input.model
      APP_A_NAME     = self.input.app_a_name
      APP_A_ENDPOINT = self.input.app_a_endpoint
      APP_B_NAME     = self.input.app_b_name
      APP_B_ENDPOINT = self.input.app_b_endpoint
      WAIT_SECONDS   = tostring(self.input.wait_seconds)
    }
    command = <<-EOT
      set -euo pipefail

      wait_for_application() {
        local app="$1"
        local deadline=$((SECONDS + WAIT_SECONDS))
        until juju show-application -m "$JUJU_MODEL" "$app" >/dev/null 2>&1; do
          if (( SECONDS >= deadline )); then
            echo "timed out waiting for Juju application $app in model $JUJU_MODEL" >&2
            juju status -m "$JUJU_MODEL" --relations --color=false || true
            exit 1
          fi
          sleep 5
        done
      }

      wait_for_application "$APP_A_NAME"
      wait_for_application "$APP_B_NAME"

      left="$APP_A_NAME:$APP_A_ENDPOINT"
      right="$APP_B_NAME:$APP_B_ENDPOINT"
      set +e
      output="$(juju integrate -m "$JUJU_MODEL" "$left" "$right" 2>&1)"
      status=$?
      set -e
      if (( status == 0 )); then
        printf '%s\n' "$output"
        exit 0
      fi
      if grep -Eiq 'already (exists|integrated)|relation already exists|integration already exists' <<<"$output"; then
        printf '%s\n' "$output"
        exit 0
      fi
      printf '%s\n' "$output" >&2
      juju status -m "$JUJU_MODEL" --relations --color=false || true
      exit "$status"
    EOT
  }

  provisioner "local-exec" {
    when        = destroy
    interpreter = ["/bin/bash", "-c"]
    environment = {
      JUJU_MODEL     = self.input.model
      APP_A_NAME     = self.input.app_a_name
      APP_A_ENDPOINT = self.input.app_a_endpoint
      APP_B_NAME     = self.input.app_b_name
      APP_B_ENDPOINT = self.input.app_b_endpoint
    }
    command = <<-EOT
      juju remove-relation -m "$JUJU_MODEL" "$APP_A_NAME:$APP_A_ENDPOINT" "$APP_B_NAME:$APP_B_ENDPOINT" >/dev/null 2>&1 || true
    EOT
  }
}
