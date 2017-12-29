package apimethods

import (
	"context"
	"fmt"
	"fsutils"
	"io"
	"userutil"

	firestore "go-genproto/googleapis/firestore/v1beta1"
)

//BatchGetDocuments ... Retrieve all documents from database
func BatchGetDocuments() {
	fmt.Println("\n:: Batch Retreive Documents ::\n")

	client, conn := fsutils.MakeFSClient()

	defer conn.Close()

	docList := []string{}

	for {
		fmt.Print("Enter Document Id (blank when finished): ")
		inp := userutil.ReadFromConsole()
		if inp != "" {
			docList = append(docList, "projects/firestoretestclient/databases/(default)/documents/GrpcTestData/"+inp)
		} else {
			break
		}
	}

	batchGetDocsReq := firestore.BatchGetDocumentsRequest{
		Database:  "projects/firestoretestclient/databases/(default)",
		Documents: docList,
	}

	stream, err := client.BatchGetDocuments(context.Background(), &batchGetDocsReq)

	if err != nil {
		fmt.Println(err)
		return
	}
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Println(err)
		}
		userutil.DrawDocument(*resp.GetFound())
	}

}
