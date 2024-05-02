package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/sirupsen/logrus"
	"google.golang.org/api/iterator"
)

func main() {

	// gather src and dst and validate usage
	sourceProjectID := flag.String("srcpid", "", "source project id")
	destProjectID := flag.String("dstpid", "", "destination project id")
	deleteSrc := flag.Bool("delete", false, "backup and delete all the keys and values")

	flag.Parse()

	// both flags are required
	if len(*sourceProjectID) == 0 || len(*destProjectID) == 0 {
		flag.Usage()
		return
	}

	// build
	ctx := context.Background()
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		logrus.Fatalf("failed to setup client: %v", err)
	}
	defer client.Close()

	logrus.Info("Reading in ignore list of psids")
	// build an ignore list there could be keys we do not want to move
	mapofIds := loadIgnoreMap()

	// List all secrets in the source project
	logrus.Println("Listing Secrets in source project:")
	srcSecrets, err := listSecrets(ctx, client, *sourceProjectID, mapofIds)
	if err != nil || len(srcSecrets) == 0 {
		logrus.Fatalf("failed to list secrets: %v", err)
	}

	// add concurrency but limit it to 5 threads
	semaphore := make(chan struct{}, 5)
	wg := &sync.WaitGroup{}

	// Create secrets in the destination project with the same names
	for _, secret := range srcSecrets {
		logrus.Printf("Creating secret %s in destination project", secret.Name)
		wg.Add(1)
		semaphore <- struct{}{} // fill the channel and block
		go func(ctx context.Context, client *secretmanager.Client, secret *secretmanagerpb.Secret) {
			defer wg.Done()
			defer func() { <-semaphore }() // consume the channel at the function return and unblock
			// fetch the value
			var value string
			if value, err = getSecretValue(ctx, client, *sourceProjectID, secret.Name); err != nil {
				logrus.Errorf("failed to get secret valueL %v", err)
				return
			}

			// backup and delete the secret
			if *deleteSrc == true {
				if err := deleteSecret(client, *sourceProjectID, secret.Name, value, secret.Labels); err != nil {
					logrus.Errorf("cannot delete the secret %s == Error: %s", secret.Name, err.Error())
				}
				return
			}

			// create the secret
			if err := createSecretWithValue(ctx, client, *destProjectID, secret.Name, value, secret.Labels); err != nil {
				logrus.Errorf("failed to create secret: %v", err)
			}
		}(ctx, client, secret)
	}
	wg.Wait()
}

type JsonItem struct {
	SecretID  string            `json:"secretID"`
	SecretKey string            `json:"secretKey"`
	Value     string            `json:"value"`
	Labels    map[string]string `json:"labels"`
}

func deleteSecret(client *secretmanager.Client, projectID, secretID, value string, labels map[string]string) error {
	secretKey := extractKeyFromPattern(secretID)
	fileName := "backup/" + secretKey + ".json"
	file, err := os.Create(fileName)
	if err != nil {
		return err
	}

	defer file.Close()

	jsonit := &JsonItem{
		SecretID:  secretID,
		SecretKey: secretKey,
		Value:     value,
		Labels:    labels,
	}

	jsonEnc, err := json.MarshalIndent(jsonit, "", "\t")
	if err != nil {
		return err
	}

	_, err = file.Write(jsonEnc)
	if err != nil {
		return err
	}

	return nil
}

func loadIgnoreMap() map[string]bool {

	// Create a map to store each line with true value
	lineMap := make(map[string]bool)

	filePath := "ignore.psids"
	os.Open(filePath)
	file, err := os.Open(filePath)

	if err != nil {
		fmt.Println("Error opening file:", err)
		return lineMap
	}
	defer file.Close() // Ensure the file is closed after reading

	// Create a scanner to read the file
	scanner := bufio.NewScanner(file)

	// Read the file line by line
	for scanner.Scan() {
		line := strings.ToLower(scanner.Text()) // the data stored in gsm is lower to test against
		lineMap[line] = true                    // Store the line as key and true as value
	}

	// Check for errors during Scan. End of file is expected and not reported by Scan as an error.
	if err := scanner.Err(); err != nil {
		logrus.Errorln("Error reading from file:", err)
		return lineMap
	}

	return lineMap
}
func listSecrets(ctx context.Context, client *secretmanager.Client, projectID string, ignoreMap map[string]bool) ([]*secretmanagerpb.Secret, error) {
	var secrets []*secretmanagerpb.Secret
	req := &secretmanagerpb.ListSecretsRequest{
		Parent: fmt.Sprintf("projects/%s", projectID),
	}
	it := client.ListSecrets(ctx, req)
	for {
		resp, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		if strings.Contains(resp.Name, "psid_") { // TODO get a key pattern config in the future
			keyName := extractKeyFromPattern(resp.Name)
			keyName = strings.ToLower(strings.Replace(keyName, "psid_", "", 1))
			if _, ok := ignoreMap[keyName]; !ok {
				//logrus.Infof("Adding secret %s from source project: Keyname: %s not found in map", resp.GetName(), keyName)
				secrets = append(secrets, resp)
			} else {
				logrus.Warnf("Skipping %s its a staging node", resp.Name)
			}
		}
	}
	return secrets, nil
}

func extractKeyFromPattern(s string) string {
	// Split the string by the "/" delimiter
	parts := strings.Split(s, "/")
	// Check if the pattern is at least as long as expected
	if len(parts) < 4 {
		logrus.Errorf("key pattern is incorrect: %v", parts)
		return ""
	}
	// The key should be the last part of the pattern
	key := parts[len(parts)-1]
	return key
}

func getSecretValue(ctx context.Context, client *secretmanager.Client, projectID, secretID string) (string, error) {

	// validate inputs and return the keyname
	secretID, err := parseKeyName(secretID)
	if err != nil {
		return "", err
	}

	// Build the access request
	accessRequest := &secretmanagerpb.AccessSecretVersionRequest{
		Name: fmt.Sprintf("projects/%s/secrets/%s/versions/latest", projectID, secretID),
	}

	// Call the API to access the secret version
	result, err := client.AccessSecretVersion(ctx, accessRequest)
	if err != nil {
		return "", err
	}

	// Data is returned as a byte slice, so convert it to a string
	return string(result.Payload.Data), nil
}

func parseKeyName(secretID string) (string, error) {

	// key will be rewritten for the new account
	secretID = extractKeyFromPattern(secretID)
	logrus.Debugf("making a create request for secretID: %s ", secretID)

	// the spec is validated since the pkg does not
	if len(secretID) == 0 || len(secretID) > 255 {
		return "", fmt.Errorf("Invalid secret: %s", secretID)
	}

	return secretID, nil
}

func createSecretWithValue(ctx context.Context, client *secretmanager.Client, projectID, secretID, value string, labels map[string]string) error {
	parent := fmt.Sprintf("projects/%s", projectID)

	secretID, err := parseKeyName(secretID)
	if err != nil {
		return err
	}

	// Create the secret
	createReq := &secretmanagerpb.CreateSecretRequest{
		Parent:   parent,
		SecretId: secretID,
		Secret: &secretmanagerpb.Secret{
			Replication: &secretmanagerpb.Replication{
				Replication: &secretmanagerpb.Replication_Automatic_{
					Automatic: &secretmanagerpb.Replication_Automatic{},
				},
			},
			Labels: labels,
		},
	}
	_, err = client.CreateSecret(ctx, createReq)
	if err != nil {
		return err
	}

	// Add the secret version with the value
	addSecretVersionReq := &secretmanagerpb.AddSecretVersionRequest{
		Parent: fmt.Sprintf("%s/secrets/%s", parent, secretID), // notice how secretID is rewritten here
		Payload: &secretmanagerpb.SecretPayload{
			Data: []byte(value),
		},
	}
	_, err = client.AddSecretVersion(ctx, addSecretVersionReq)

	return err
}
