package js

import "github.com/dop251/goja"

func ToJSON(vm *goja.Runtime, v goja.Value) (string, error) {
	jsonObj := vm.Get("JSON").ToObject(vm)
	stringify, ok := goja.AssertFunction(jsonObj.Get("stringify"))
	if !ok {
		return "", nil
	}

	res, err := stringify(goja.Undefined(), v)
	if err != nil {
		return "", err
	}

	return res.String(), nil
}
