package apiclient_test

import (
	"github.com/veilon/veilon-app/veilond/internal/apiclient"
	"github.com/veilon/veilon-app/veilond/internal/core"
)

var _ core.BillingAPI = (*apiclient.Client)(nil)
