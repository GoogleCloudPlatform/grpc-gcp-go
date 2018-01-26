package environment

//Env ... Current environment
//  Contents:
//      transactionId - track transaction ids for commits and rollbacks
//
type Env struct {
	TransactionId []byte
}

var (
	env *Env
)

//SetEnvironment ... Update current environment
func SetEnvironment(transactionId []byte) *Env {

	env = &Env{
		TransactionId: transactionId,
	}
	return env
}

//GetEnvironment ... Return current environment
func GetEnvironment() *Env {
	if env != nil {
		return env
	}
	return nil
}

//ClearEnvironment ... Clean up environment
func ClearEnvironment() *Env {
	env = nil
	return env
}
