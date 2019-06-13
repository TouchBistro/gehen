# Gehen
Gehen is a mininal version update aid for ECS services. It assumes the service is existing and running and will update the container tags to a new gitsha, then check for up to 10 minutes and exit when confirming the new version is deployed ("Checks the Server header of the url response past the '-' key against the gitsha provided")


```./gehen --help
Usage of ./gehen:
  -cluster string
    	The full cluster ARN to deploy this service to
  -gitsha string
    	The gitsha of the version to be deployed
  -service string
    	The service name running this service on ECS
  -url string
    	The URL to check for the deployed version```