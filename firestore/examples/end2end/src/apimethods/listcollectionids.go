package apimethods

import (
	"context"
	"fmt"
	"fsutils"
	firestore "go-genproto/googleapis/firestore/v1beta1"
)

//ListCollectionIds ... List all collection IDs from a database
func ListCollectionIds() {
	fmt.Println("\n:: Listing All Collection Ids ::\n")

	client, conn := fsutils.MakeFSClient()

	defer conn.Close()

	listCollectionIdReq := firestore.ListCollectionIdsRequest{
		Parent: "projects/firestoretestclient/databases/(default)",
	}

	resp, err := client.ListCollectionIds(context.Background(), &listCollectionIdReq)

	if err != nil {
		fmt.Println(err)
		return
	}

	for _, cid := range resp.CollectionIds {
		fmt.Println(cid)
	}

	return

}
