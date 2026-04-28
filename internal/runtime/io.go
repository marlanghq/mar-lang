package runtime

import (
	"bufio"
	"fmt"
	"os"
)

// ioBuiltins returns runtime functions for basic I/O. All return Effects.
func ioBuiltins() map[string]Value {
	return map[string]Value{
		// IO.print : String -> Effect e ()
		"ioPrint": nativeFn(1, func(args []Value) (Value, error) {
			s, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("IO.print: expected String")
			}
			return VEffect{
				Tag: "print",
				Run: func() (Value, error) {
					fmt.Print(s.V)
					return VUnit{}, nil
				},
			}, nil
		}),
		// IO.println : String -> Effect e ()
		"ioPrintln": nativeFn(1, func(args []Value) (Value, error) {
			s, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("IO.println: expected String")
			}
			return VEffect{
				Tag: "println",
				Run: func() (Value, error) {
					fmt.Println(s.V)
					return VUnit{}, nil
				},
			}, nil
		}),
		// IO.readLine : Effect e String
		"ioReadLine": VEffect{
			Tag: "readLine",
			Run: func() (Value, error) {
				scanner := bufio.NewScanner(os.Stdin)
				if !scanner.Scan() {
					return VString{V: ""}, nil
				}
				return VString{V: scanner.Text()}, nil
			},
		},
	}
}
