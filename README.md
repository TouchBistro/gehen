# Gehen
Gehen is a mininal version update aid for ECS services. It assumes the service is existing and running and will update the container tags to a new gitsha, then check for up to 10 minutes and exit when confirming the new version is deployed ("Checks the Server header of the url response past the '-' key against the gitsha provided")

```
Usage of ./gehen:
  -gitsha string
        The gitsha of the version to be deployed
  -path string
        The path to a gehen.yml config file
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

## Contributing

See [contributing](CONTRIBUTING.md) for instructions on how to contribute to `gehen`. PRs welcome!

## License

MIT © TouchBistro, see [LICENSE](LICENSE) for details.
