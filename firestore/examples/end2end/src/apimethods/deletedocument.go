package apimethods

import (
	"context"
	"fmt"
	"fsutils"
	firestore "go-genproto/googleapis/firestore/v1beta1"
	"userutil"
)

//DeleteDocument ... Delete a document from database
func DeleteDocument() {
	fmt.Println("\n:: Deleting a Document ::\n")
	client, conn := fsutils.MakeFSClient()

	defer conn.Close()

	fmt.Print("Enter Document Name: ")
	docName := "projects/firestoretestclient/databases/(default)/documents/GrpcTestData/" + userutil.ReadFromConsole()

	delDocRequest := firestore.DeleteDocumentRequest{
		Name: docName,
	}

	_, err := client.DeleteDocument(context.Background(), &delDocRequest)

	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println("Successfully Deleted ", docName)
}
