package apimethods

import (
	"context"
	"fmt"
	"fsutils"
	firestore "go-genproto/googleapis/firestore/v1beta1"
	"userutil"
)

//ListDocuments ... List all Documents from a Database
func ListDocuments() {
	fmt.Println("\n:: Listing All Documents ::\n")

	client, conn := fsutils.MakeFSClient()

	defer conn.Close()

	listDocsRequest := firestore.ListDocumentsRequest{
		Parent: "projects/firestoretestclient/databases/(default)",
	}

	//bgd_client, err := client.BatchGetDocuments(context.Background(), &getDocsRequest, grpc.FailFast(true))
	resp, err := client.ListDocuments(context.Background(), &listDocsRequest)
	if err != nil {
		fmt.Println(err)
	}
	for _, doc := range resp.Documents {
		userutil.DrawDocument(*doc)
	}

	return
}
