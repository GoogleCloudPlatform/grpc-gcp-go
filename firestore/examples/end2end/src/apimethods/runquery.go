package apimethods

import (
	"context"
	"fmt"
	"fsutils"
	firestore "go-genproto/googleapis/firestore/v1beta1"
	"io"
	"userutil"
)

//RunQuery ... Run a Structured Query
func RunQuery() {
	fmt.Println("\n:: Running a Query ::\n")

	client, conn := fsutils.MakeFSClient()

	defer conn.Close()

	fmt.Print("Enter field to query: ")
	fieldPath := userutil.ReadFromConsole()

	runQueryQuery := firestore.StructuredQuery{
		Select: &firestore.StructuredQuery_Projection{
			Fields: []*firestore.StructuredQuery_FieldReference{
				&firestore.StructuredQuery_FieldReference{
					FieldPath: fieldPath,
				},
			},
		},
	}

	runStructQueryReq := firestore.RunQueryRequest_StructuredQuery{
		StructuredQuery: &runQueryQuery,
	}

	runQueryReq := firestore.RunQueryRequest{
		Parent:    "projects/firestoretestclient/databases/(default)/documents",
		QueryType: &runStructQueryReq,
	}

	call, err := client.RunQuery(context.Background(), &runQueryReq)

	if err != nil {
		fmt.Println("Error getting QueryClient: ", err)
	}

	for {
		results, err := call.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Println("Error received from QueryClient: ", err)
		}
		userutil.DrawDocument(*results.Document)

	}

}
