package apimethods

import (
	"context"
	"environment"
	"fmt"
	"fsutils"

	firestore "go-genproto/googleapis/firestore/v1beta1"
)

//Commit ... Hitting Commit API
func Commit() {
	fmt.Println("\n:: Hitting Commit API ::\n")
	client, conn := fsutils.MakeFSClient()

	defer conn.Close()

	env := environment.GetEnvironment()

	if env == nil {
		fmt.Println("\nNo transaction to commit, returning...")
		return
	}

	transactionId := env.TransactionId

	commitReq := firestore.CommitRequest{
		Database:    "projects/firestoretestclient/databases/(default)",
		Transaction: transactionId,
	}
	resp, err := client.Commit(context.Background(), &commitReq)

	if err != nil {
		fmt.Println(err)
		return
	}
	environment.ClearEnvironment()
	fmt.Println("Successful commit! ", resp)
}
