package main

import (
	"context"
	"fmt"
	"log"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"google.golang.org/api/iterator"
	secretmanagerpb "google.golang.org/genproto/googleapis/cloud/secretmanager/v1"
)

func main() {
	ctx := context.Background()
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		log.Fatalf("failed to setup client: %v", err)
	}
	defer client.Close()

	// Replace 'your-source-project-id' with your source Google Cloud Project ID
	sourceProjectID := "your-source-project-id"
	destProjectID := "your-destination-project-id"

	// List all secrets in the source project
	fmt.Println("Listing Secrets in source project:")
	srcSecrets, err := listSecrets(ctx, client, sourceProjectID)
	if err != nil {
		log.Fatalf("failed to list secrets: %v", err)
	}

	// Assuming the user switches Google Cloud project authorization here
	fmt.Println("Please switch your Google Cloud credentials now to access the destination project.")

	// Create secrets in the destination project with the same names
	for _, secret := range srcSecrets {
		fmt.Printf("Creating secret %s in destination project\n", secret.Name)
		if err := createSecret(ctx, client, destProjectID, secret.Name); err != nil {
			log.Printf("failed to create secret: %v", err)
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
		secrets = append(secrets, resp)
	}
	return secrets, nil
}

func createSecret(ctx context.Context, client *secretmanager.Client, projectID, secretID string) error {
	req := &secretmanagerpb.CreateSecretRequest{
		Parent:   fmt.Sprintf("projects/%s", projectID),
		SecretId: secretID,
		Secret: &secretmanagerpb.Secret{
			Replication: &secretmanagerpb.Replication{
				Replication: &secretmanagerpb.Replication_Automatic_{
					Automatic: &secretmanagerpb.Replication_Automatic{},
				},
			},
		},
	}
	_, err := client.CreateSecret(ctx, req)
	return err
}

