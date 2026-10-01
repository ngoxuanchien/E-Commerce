package httpadapter_test

import (
	"os"
	"testing"

	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/testsupport"
)

func TestMain(m *testing.M) { os.Exit(testsupport.RunWithPostgres(m)) }
