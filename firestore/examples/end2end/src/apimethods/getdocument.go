package apimethods

import (
	"context"
	"fmt"
	"fsutils"
	firestore "go-genproto/googleapis/firestore/v1beta1"
	"userutil"
)

//GetDocument ... Retrieve a specific Document
func GetDocument() {
	fmt.Println("\n:: Getting A Document ::\n")

	client, conn := fsutils.MakeFSClient()

	defer conn.Close()

	fmt.Print("Enter Document Name: ")
	docName := "projects/firestoretestclient/databases/(default)/documents/GrpcTestData/" + userutil.ReadFromConsole()

	getDocRequest := firestore.GetDocumentRequest{
		Name: docName,
	}

	resp, err := client.GetDocument(context.Background(), &getDocRequest)

	if err != nil {
		fmt.Println(err)
	}

	userutil.DrawDocument(*resp)

	return
}
