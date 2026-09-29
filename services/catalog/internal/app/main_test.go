package app_test

import (
	"os"
	"testing"

	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/testsupport"
)

func TestMain(m *testing.M) { os.Exit(testsupport.RunWithPostgres(m)) }
