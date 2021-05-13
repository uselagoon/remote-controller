# Harbor Integration

The Lagoon build deploy controller supports injection of Harbor credentials, and manages the rotation of robot account credentials to ensure they are always up to date.

This feature allows controllers in multiple clusters to be able to point to a more geographically preferred Harbor to allow for better push/pull performance or latency.

## Configuration

* `--enable-harbor` "Flag to enable this controller to talk to a specific harbor."
* `--harbor-url` "The URL for harbor, this is where images will be pushed."
* `--harbor-api` "The URL for harbor API."
* `--harbor-username` "The username for accessing harbor."
* `--harbor-password` "The password for accessing harbor."
* `--harbor-robot-prefix` "The default prefix for robot accounts, will usually be 'robot$'."
* `--harbor-robot-delete-disabled` "Tells harbor to delete any disabled robot accounts and re-create them if required."
* `--harbor-enable-project-webhook` "Tells the controller to add Lagoon webhook policies to harbor projects."
* `--harbor-expiry-interval` "The number of days or hours (eg 24h or 30d) before expiring credentials to re-fresh."
* `--harbor-rotate-interval` "The number of days or hours (eg 24h or 30d) to force refresh if required."
* `--harbor-robot-account-expiry` "The number of days or hours (eg 24h or 30d) to force refresh if required."
* `--harbor-credential-cron` "Cron definition for how often to run harbor credential rotations"
* `--harbor-lagoon-webhook` "The webhook URL to add for Lagoon, this is where events notifications will be posted"
* `--harbor-webhook-eventtypes` "The event types to use for the Lagoon webhook"

A lot of these have defaults that are probably ok to leave.

## Support in Lagoon

The controller will overwrite, or add, the following environment variables directly into the Project Variables that would be injected by Lagoon core, this is to allow the existing functionality in the `kubectl-build-deploy-dind` image to work without changes.

```
INTERNAL_REGISTRY_URL
INTERNAL_REGISTRY_USERNAME
INTERNAL_REGISTRY_PASSWORD
```

## Disable on Lagoon side

There is a way to prevent the controller from injecting the localised Harbor credentials, by adding the following to the Lagoons Project or specific Environment variable in the Lagoon API. This way the controller knows to use what was provided by Lagoon.

```
INTERNAL_REGISTRY_SOURCE_LAGOON=true
```