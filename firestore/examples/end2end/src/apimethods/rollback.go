package apimethods

import (
	"context"
	"environment"
	"fmt"
	"fsutils"
	firestore "go-genproto/googleapis/firestore/v1beta1"
)

//Rollback ... Call the Rollback API
func Rollback() {
	fmt.Println("\n:: Calling Rollback API ::\n")
	client, conn := fsutils.MakeFSClient()

	defer conn.Close()

	env := environment.GetEnvironment()

	if env == nil {
		fmt.Println("\nNo transaction to rollback, returning...")
		return
	}

	transactionId := env.TransactionId

	rollbackReq := firestore.RollbackRequest{
		Database:    "projects/firestoretestclient/databases/(default)",
		Transaction: transactionId,
	}
	resp, err := client.Rollback(context.Background(), &rollbackReq)

	if err != nil {
		fmt.Println(err)
		return
	}
	environment.ClearEnvironment()
	fmt.Println("Successful rollback! ", resp)
}
