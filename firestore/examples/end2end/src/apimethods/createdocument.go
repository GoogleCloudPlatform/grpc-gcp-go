package apimethods

import (
	"context"
	"fmt"
	"fsutils"
	firestore "go-genproto/googleapis/firestore/v1beta1"
	"userutil"
)

//CreateDocument ... Create a New Document
func CreateDocument() {
	fmt.Println("\n:: Creating a New Document ::\n")

	client, conn := fsutils.MakeFSClient()

	defer conn.Close()

	fields := make(map[string]*firestore.Value)

	fmt.Print("Enter Document Name: ")
	docId := userutil.ReadFromConsole()

	for {
		fmt.Println("Enter Field Name (blank when finished): ")
		fieldName := userutil.ReadFromConsole()
		if fieldName != "" {
			fmt.Println("Enter Field Value: ")
			fieldValString := userutil.ReadFromConsole()
			fields[fieldName] = &firestore.Value{
				ValueType: &firestore.Value_StringValue{
					StringValue: fieldValString,
				},
			}
		} else {
			break
		}
	}

	doc := firestore.Document{
		Fields: fields,
	}

	createDocRequest := firestore.CreateDocumentRequest{
		Parent:       "projects/firestoretestclient/databases/(default)/documents",
		CollectionId: "GrpcTestData",
		DocumentId:   docId,
		Document:     &doc,
	}

	resp, err := client.CreateDocument(context.Background(), &createDocRequest)
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println("Successfully created document!")
	userutil.DrawDocument(*resp)

}
