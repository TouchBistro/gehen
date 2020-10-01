# Gehen
Gehen is a mininal version update aid for ECS services.

## How it works
#### Deployment
Gehen assumes that docker images are tagged with the Git SHA of the corresponding commit. This makes it easy to identify which version of the code is in a given image.

Gehen will register a new task definition in ECS for your service and update the image tag to be the new Git SHA provided. It will then update the ECS service to use the new task definition and trigger a new deployment.

**NOTE:** Gehen assumes the service already exists in ECS. It will not create services for you.

#### Deploy Check
Gehen will keep pinging your service to see if the new version has been deployed. For this to work your service must send the [Server](https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Server) header with the format `*-GITSHA` where `GITSHA` is the Git SHA you provided to Gehen to update the service to.

Gehen will keep hitting the URL provided for your services until it either sees the new Git SHA in the header, or times out. The default timeout duration is 5 minutes.

#### Drain Check
Once Gehen sees that the new version of the service has deployed it will wait for the old version(s) to drain, i.e. stop running. The default timeout duration is 5 minutes.

## Usage

```
Usage of ./gehen:
  -gitsha string
        The gitsha of the version to be deployed
  -path string
        The path to a gehen.yml config file (default "gehen.yml")
  -version
        Prints the current gehen version
```

## Configuration

Gehen is configured through a `gehen.yml` file. This contains the list of ECS services to deploy to. You can specify multiple services to deploy the service to multiple environments.

The schema is as follows:

```yaml
services: # A map of services
  <service-name>: # The name of the ECS service
    cluster: string # The ECS cluster the service is in
    url: string # The URL to use to check that the new version has been deployed
```

An example config is provided in [gehen.example.yml](gehen.example.yml).

## Contributing

See [contributing](CONTRIBUTING.md) for instructions on how to contribute to `gehen`. PRs welcome!

## License

MIT © TouchBistro, see [LICENSE](LICENSE) for details.
