package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/sirupsen/logrus"
	"google.golang.org/api/iterator"
)

func main() {

	// gather src and dst and validate usage
	sourceProjectID := flag.String("srcpid", "", "source project id")
	destProjectID := flag.String("dstpid", "", "destination project id")
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

	// List all secrets in the source project
	logrus.Println("Listing Secrets in source project:")
	srcSecrets, err := listSecrets(ctx, client, *sourceProjectID)
	if err != nil || len(srcSecrets) == 0 {
		logrus.Fatalf("failed to list secrets: %v", err)
	}

	// Create secrets in the destination project with the same names
	for _, secret := range srcSecrets {
		logrus.Printf("Creating secret %s in destination project", secret.Name)

		// fetch the value
		var value string
		if value, err = getSecretValue(ctx, client, *sourceProjectID, secret.Name); err != nil {
			logrus.Errorf("failed to get secret valueL %v", err)
			continue
		}

		// create the secret
		if err := createSecretWithValue(ctx, client, *destProjectID, secret.Name, value, secret.Labels); err != nil {
			logrus.Errorf("failed to create secret: %v", err)
		}
	}
}

func listSecrets(ctx context.Context, client *secretmanager.Client, projectID string) ([]*secretmanagerpb.Secret, error) {
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
			logrus.Debugf("Adding secret %s from source project", resp.GetName())
			secrets = append(secrets, resp)
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
