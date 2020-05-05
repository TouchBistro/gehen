package check

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/pkg/errors"
)

func fetchRevisionSha(url string) (string, error) {
	resp, err := http.Get(url)
	if resp != nil {
		defer resp.Body.Close()
	}

	if err != nil {
		return "", errors.Errorf("Failed to HTTP GET %s", url)
	}

	// Check status
	if resp.StatusCode != 200 {
		return "", errors.Errorf("Received non 200 status from %s", url)
	}

	// Check if revision sha is in the http Server header.
	if header := resp.Header.Get("Server"); header != "" {
		// TODO: use a regular expression
		t := strings.Split(header, "-")
		if len(t) > 1 {
			return t[len(t)-1], nil
		}
	}

	// Check if revision sha is in the body
	bodySha, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", errors.Errorf("Failed to parse body from %s", url)
	}

	return string(bodySha), nil
}

func Deploy(url, expectedSha string) (ok bool, err error) {
	fetchedSha, err := fetchRevisionSha(url)
	if err != nil {
		return false, errors.Wrapf(err, "Could not parse a gitsha version from header or body at %s\n", url)
	}

	log.Printf("Got %s from %s\n", fetchedSha, url)
	if len(fetchedSha) > 7 && strings.HasPrefix(expectedSha, fetchedSha) {
		return true, nil
	}

	return false, nil
}

func SmokeTest(url string) error {
	client := &http.Client{}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return errors.Errorf("Failed to build HTTP request for %s", url)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", os.Getenv("CHECKER_BEARER_TOKEN")))
	resp, err := client.Do(req)
	if resp != nil {
		defer resp.Body.Close()
	}

	if err != nil {
		return errors.Errorf("Failed to HTTP GET %s", url)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return errors.Errorf("Could not parse body from %s", url)
	}

	if resp.StatusCode != 200 {
		return errors.Errorf("Error HTTP Status %d returned from smoke test with error %s", resp.StatusCode, string(body))
	}

	return nil
}
