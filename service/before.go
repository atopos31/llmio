package service

import (
	"errors"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type before struct {
	model            string
	stream           bool
	toolCall         bool
	structuredOutput bool
	raw              []byte
}

type Beforer func(data []byte) (*before, error)

func BeforerOpenAI(data []byte) (*before, error) {
	model := gjson.GetBytes(data, "model").String()
	if model == "" {
		return nil, errors.New("model is empty")
	}
	stream := gjson.GetBytes(data, "stream").Bool()
	if stream {
		// 为processTee记录usage添加选项 PS:很多客户端只会开启stream 而不会开启include_usage
		newData, err := sjson.SetBytes(data, "stream_options", struct {
			IncludeUsage bool `json:"include_usage"`
		}{IncludeUsage: true})
		if err != nil {
			return nil, err
		}
		data = newData
	}
	var toolCall bool
	tools := gjson.GetBytes(data, "tools")
	if tools.Exists() && len(tools.Array()) != 0 {
		toolCall = true
	}
	var structuredOutput bool
	if gjson.GetBytes(data, "response_format").Exists() {
		structuredOutput = true
	}
	return &before{
		model:            model,
		stream:           stream,
		toolCall:         toolCall,
		structuredOutput: structuredOutput,
		raw:              data,
	}, nil
}

func BeforerAnthropic(data []byte) (*before, error) {
	model := gjson.GetBytes(data, "model").String()
	if model == "" {
		return nil, errors.New("model is empty")
	}
	stream := gjson.GetBytes(data, "stream").Bool()
	var toolCall bool
	tools := gjson.GetBytes(data, "tools")
	if tools.Exists() && len(tools.Array()) != 0 {
		toolCall = true
	}
	return &before{
		model:            model,
		stream:           stream,
		toolCall:         toolCall,
		structuredOutput: toolCall,
		raw:              data,
	}, nil
}
