package sora

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

func TestDefaultSecondsByModel(t *testing.T) {
	require.Equal(t, 6, defaultSecondsByModel("grok-imagine-video"))
	require.Equal(t, 4, defaultSecondsByModel("sora-2"))
}

func TestValidateModelSpecificSeconds(t *testing.T) {
	require.Nil(t, validateModelSpecificSeconds(relaycommon.TaskSubmitReq{
		Model:   "grok-imagine-video",
		Seconds: "6",
	}))
	require.Nil(t, validateModelSpecificSeconds(relaycommon.TaskSubmitReq{
		Model: "grok-imagine-video",
	}))
	require.NotNil(t, validateModelSpecificSeconds(relaycommon.TaskSubmitReq{
		Model:   "grok-imagine-video",
		Seconds: "4",
	}))
	require.Nil(t, validateModelSpecificSeconds(relaycommon.TaskSubmitReq{
		Model:   "sora-2",
		Seconds: "4",
	}))
}
