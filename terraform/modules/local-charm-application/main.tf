resource "terraform_data" "this" {
  input = {
    app_name   = var.app_name
    model_name = var.model_name
  }

  triggers_replace = {
    charm_sha  = filesha256(var.charm_file)
    app_image  = var.app_image
    config_sha = sha256(jsonencode(var.config))
  }

  provisioner "local-exec" {
    command = "bash ${path.module}/scripts/apply-local-charm.sh"

    environment = {
      APP_CONFIG_JSON = jsonencode(var.config)
      APP_IMAGE       = var.app_image
      APP_NAME        = var.app_name
      CHARM_FILE      = var.charm_file
      MODEL_NAME      = var.model_name
      WAIT_TIMEOUT    = var.wait_timeout
    }
  }

  provisioner "local-exec" {
    when    = destroy
    command = "juju remove-application \"$APP_NAME\" -m \"$MODEL_NAME\" --destroy-storage --force --no-wait || true"

    environment = {
      APP_NAME   = self.input.app_name
      MODEL_NAME = self.input.model_name
    }
  }
}
