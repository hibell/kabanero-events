/*
Copyright 2019 IBM Corporation

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"encoding/json"
	"github.com/google/go-github/github"
	"github.com/kabanero-io/kabanero-events/internal/utils"
	"io/ioutil"
	"k8s.io/klog"
	"net/http"
	"os"

	"context"
	"fmt"
)

const (
	tlsCertPath = "/etc/tls/tls.crt"
	tlsKeyPath  = "/etc/tls/tls.key"
)

/* HTTP listener */
func listenerHandler(writer http.ResponseWriter, req *http.Request) {

	header := req.Header
	klog.Infof("Received request. Header: %v", header)

	var body = req.Body

	defer body.Close()
	bytes, err := ioutil.ReadAll(body)
	if err != nil {
		klog.Errorf("Webhook listener can not read body. Error: %v", err)
	} else {
		klog.Infof("Webhook listener received body: %v", string(bytes))
	}

	var bodyMap map[string]interface{}
	err = json.Unmarshal(bytes, &bodyMap)
	if err != nil {
		klog.Errorf("Unable to unarmshal json body: %v", err)
		return
	}

	message := make(map[string]interface{})
	message[HEADER] = map[string][]string(header)
	message[BODY] = bodyMap

	bytes, err = json.Marshal(message)
	if err != nil {
		klog.Errorf("Unable to marshall as JSON: %v, type %T", message, message)
		return
	}

	destNode := eventProviders.GetEventDestination(WEBHOOKDESTINATION)
	if destNode == nil {
		klog.Errorf("Unable to find an eventDestination with the name '%s'. Verify that it has been defined.", WEBHOOKDESTINATION)
		return
	}
	provider := eventProviders.GetMessageProvider(destNode.ProviderRef)
	if provider == nil {
		klog.Errorf("Unable to find a messageProvider with the name '%s'. Verify that is has been defined.", destNode.ProviderRef)
		return
	}

	err = provider.Send(destNode, bytes, nil)
	if err != nil {
		klog.Errorf("Unable to send webhook message. Error: %v", err)
		return
	}
	if err != nil {
		klog.Errorf("Error processing webhook message: %v", err)
	}
	writer.WriteHeader(http.StatusAccepted)
}

func newListener() error {
	http.HandleFunc("/webhook", listenerHandler)

	if disableTLS {
		klog.Infof("Starting listener on port 9080")
		err := http.ListenAndServe(":9080", nil)
		return err
	}

	// Setup TLS listener
	if _, err := os.Stat(tlsCertPath); os.IsNotExist(err) {
		klog.Fatalf("TLS certificate '%s' not found: %v", tlsCertPath, err)
		return err
	}

	if _, err := os.Stat(tlsKeyPath); os.IsNotExist(err) {
		klog.Fatalf("TLS private key '%s' not found: %v", tlsKeyPath, err)
		return err
	}

	klog.Infof("Starting listener on port 9443")
	err := http.ListenAndServeTLS(":9443", tlsCertPath, tlsKeyPath, nil)
	return err
}

/* Get the repository's information from from github message body: name, owner, html_url, and ref */
func getRepositoryInfo(body map[string]interface{}, repositoryEvent string) (string, string, string, string, error) {

	ref := ""
	if repositoryEvent == "push" {
		// use SHA for ref
		afterObj, ok := body["after"]
		if !ok {
			return "", "", "", "", fmt.Errorf("unable to find after for push webhook message")
		}
		ref, ok = afterObj.(string)
		if !ok {
			return "", "", "", "", fmt.Errorf("after for push webhook message (%s) is not a string but %T", afterObj, afterObj)
		}
	} else if repositoryEvent == "pull_request" {
		// use pull_request.head.sha
		prObj, ok := body["pull_request"]
		if !ok {
			return "", "", "", "", fmt.Errorf("unable to find pull_request in webhook message")
		}
		prMap, ok := prObj.(map[string]interface{})
		if !ok {
			return "", "", "", "", fmt.Errorf("pull_request in webhook message is of type %T, not map[string]interface{}", prObj)
		}
		headObj, ok := prMap["head"]
		if !ok {
			return "", "", "", "", fmt.Errorf("pull_request in webhook message does not contain head")
		}
		head, ok := headObj.(map[string]interface{})
		if !ok {
			return "", "", "", "", fmt.Errorf("pull_request head not map[string]interface{}, but is %T", headObj)
		}

		shaObj, ok := head["sha"]
		if !ok {
			return "", "", "", "", fmt.Errorf("pull_request.head.sha not found")
		}
		ref, ok = shaObj.(string)
		if !ok {
			return "", "", "", "", fmt.Errorf("pull_request merge_commit_sha in webhook message not a string: %v", shaObj)
		}
	}

	repositoryObj, ok := body["repository"]
	if !ok {
		return "", "", "", "", fmt.Errorf("unable to find repository in webhook message")
	}
	repository, ok := repositoryObj.(map[string]interface{})
	if !ok {
		return "", "", "", "", fmt.Errorf("webhook message repository object not map[string]interface{}: %v", repositoryObj)
	}

	nameObj, ok := repository["name"]
	if !ok {
		return "", "", "", "", fmt.Errorf("webhook message repository name not found")
	}
	name, ok := nameObj.(string)
	if !ok {
		return "", "", "", "", fmt.Errorf("webhook message repository name not a string: %v", nameObj)
	}

	ownerMapObj, ok := repository["owner"]
	if !ok {
		return "", "", "", "", fmt.Errorf("webhook message repository owner not found")
	}
	ownerMap, ok := ownerMapObj.(map[string]interface{})
	if !ok {
		return "", "", "", "", fmt.Errorf("webhook message repository owner object not map[string]interface{}: %v", ownerMapObj)
	}
	ownerObj, ok := ownerMap["login"]
	if !ok {
		return "", "", "", "", fmt.Errorf("webhook message repository owner login not found")
	}
	owner, ok := ownerObj.(string)
	if !ok {
		return "", "", "", "", fmt.Errorf("webhook message repository owner login not string : %v", ownerObj)
	}

	htmlURLObj, ok := repository["html_url"]
	if !ok {
		return "", "", "", "", fmt.Errorf("webhook message repository html_url not found")
	}
	htmlURL, ok := htmlURLObj.(string)
	if !ok {
		return "", "", "", "", fmt.Errorf("webhook message html_url not string: %v", htmlURL)
	}

	return owner, name, htmlURL, ref, nil
}

// Download file and return: bytes of the file, true if file texists, and any error
func downloadFileFromGithub(owner, repository, fileName, ref, githubURL, user, token string, isEnterprise bool) ([]byte, bool, error) {

	if klog.V(5) {
		klog.Infof("downloadFileFromGithub %v, %v, %v, %v, %v, %v, %v", owner, repository, fileName, ref, githubURL, user, isEnterprise)
	}

	ctx := context.Background()

	tp := github.BasicAuthTransport{
		Username: user,
		Password: token,
	}
	/*
		tokenService := oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: token},
		)
		tokenClient := oauth2.NewClient(ctx, tokenService)
	*/

	var err error
	var client *github.Client
	if isEnterprise {
		githubURL = githubURL + "/api/v3"
		client, err = github.NewEnterpriseClient(githubURL, githubURL, tp.Client())
		if err != nil {
			return nil, false, err
		}
	} else {
		client = github.NewClient(tp.Client())
	}

	var options *github.RepositoryContentGetOptions
	if ref != "" {
		options = &github.RepositoryContentGetOptions{ref}
	}

	/*
	       rc, err := client.Repositories.DownloadContents(ctx, owner, repository, fileName, options)
	       if err != nil {
	   		fmt.Printf("Error type: %T, value: %v\n", err, err)
	           return nil, false, err
	       }
	       defer rc.Close()
	   	buf, err := ioutil.ReadAll(rc)
	*/
	fileContent, _, resp, err := client.Repositories.GetContents(ctx, owner, repository, fileName, options)
	if resp.Response.StatusCode == 200 {
		if fileContent != nil {
			if fileContent.Content == nil {
				return nil, true, fmt.Errorf("content for %v/%v/%v is nil", owner, repository, fileName)
			}

			content, err := fileContent.GetContent()
			if err != nil {
				klog.Infof("download File Form Github error %v", err)
			} else {
				klog.Infof("download File from Github: buffer %v", content)
			}
			return []byte(content), true, err
		}
		/* some other errors */
		return nil, false, fmt.Errorf("unable to download %v/%v/%v: not a file", owner, repository, fileName)
	} else if resp.Response.StatusCode == 400 {
		/* does not exist */
		return nil, false, nil
	} else {
		/* some other errors */
		return nil, false, fmt.Errorf("unable to download %v/%v/%v, http error %v", owner, repository, fileName, resp.Response.Status)
	}

}

/* Download YAML from Repository.
header: HTTP header from webhook
bodyMap: HTTP  message body from webhook
*/
func downloadYAML(header map[string][]string, bodyMap map[string]interface{}, fileName string) (map[string]interface{}, bool, error) {

	hostHeader, isEnterprise := header[http.CanonicalHeaderKey("x-github-enterprise-host")]
	var host string
	if !isEnterprise {
		host = "github.com"
	} else {
		host = hostHeader[0]
	}

	repositoryEvent := header["X-Github-Event"][0]

	owner, name, htmlURL, ref, err := getRepositoryInfo(bodyMap, repositoryEvent)
	if err != nil {
		return nil, false, fmt.Errorf("unable to get repository owner, name, or html_url from webhook message: %v", err)
	}

	user, token, _, err := utils.GetURLAPIToken(dynamicClient, webhookNamespace, htmlURL)
	if err != nil {
		return nil, false, fmt.Errorf("unable to get user/token secrets for URL %v", htmlURL)
	}

	githubURL := "https://" + host

	bytes, found, err := downloadFileFromGithub(owner, name, fileName, ref, githubURL, user, token, isEnterprise)
	if err != nil {
		return nil, found, err
	}
	retMap, err := utils.YAMLToMap(bytes)
	return retMap, found, err
}
