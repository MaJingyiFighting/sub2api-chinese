package handler

import (
	"testing"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// A small test to ensure compilation
func TestGatewayHandler_CodingPlanResponses_Compiles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	assert.True(t, true)
}
