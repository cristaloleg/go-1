package main

import (
	"bytes"
	"fmt"
	"go/format"
	"io/ioutil"
	"os"
	"path"
	"strings"

	"github.com/varlink/go/varlink/idl"
)

func writeType(b *bytes.Buffer, t *idl.Type, json bool, ident int) {
	switch t.Kind {
	case idl.TypeBool:
		b.WriteString("bool")

	case idl.TypeInt:
		b.WriteString("int64")

	case idl.TypeFloat:
		b.WriteString("float64")

	case idl.TypeString, idl.TypeEnum:
		b.WriteString("string")

	case idl.TypeObject:
		b.WriteString("json.RawMessage")

	case idl.TypeArray:
		b.WriteString("[]")
		writeType(b, t.ElementType, json, ident)

	case idl.TypeMap:
		b.WriteString("map[string]")
		writeType(b, t.ElementType, json, ident)

	case idl.TypeMaybe:
		b.WriteString("*")
		writeType(b, t.ElementType, json, ident)

	case idl.TypeAlias:
		b.WriteString(t.Alias)

	case idl.TypeStruct:
		if len(t.Fields) == 0 {
			b.WriteString("struct{}")
		} else {
			b.WriteString("struct {\n")
			for _, field := range t.Fields {
				for i := 0; i < ident+1; i++ {
					b.WriteString("\t")
				}

				b.WriteString(strings.Title(field.Name) + " ")
				writeType(b, field.Type, json, ident+1)
				if json {
					b.WriteString(" `json:\"" + field.Name)
					if field.Type.Kind == idl.TypeMaybe {
						b.WriteString(",omitempty")
					}
					b.WriteString("\"`")
				}
				b.WriteString("\n")
			}
			for i := 0; i < ident; i++ {
				b.WriteString("\t")
			}
			b.WriteString("}")
		}
	}
}

func generateTemplate(description string) (string, []byte, error) {
	description = strings.TrimRight(description, "\n")

	midl, err := idl.New(description)
	if err != nil {
		return "", nil, err
	}

	pkgname := strings.Replace(midl.Name, ".", "", -1)

	var b bytes.Buffer
	b.WriteString("// Generated with github.com/varlink/go/cmd/varlink-go-interface-generator\n")
	b.WriteString("package " + pkgname + "\n\n")
	b.WriteString("@IMPORTS@\n\n")

	b.WriteString("// Type declarations\n")
	for _, a := range midl.Aliases {
		b.WriteString("type " + a.Name + " ")
		writeType(&b, a.Type, true, 0)
		b.WriteString("\n\n")
	}

	b.WriteString("// Client method calls\n")
	for _, m := range midl.Methods {
		b.WriteString("func " + m.Name + "(c__ *varlink.Connection, flags__ int")
		for _, field := range m.In.Fields {
			b.WriteString(", " + field.Name + "_ ")
			writeType(&b, field.Type, false, 1)
		}
		b.WriteString(") error {\n")
		if len(m.In.Fields) > 0 {
			b.WriteString("\tvar in ")
			writeType(&b, m.In, true, 1)
			b.WriteString("\n")
			for _, field := range m.In.Fields {
				switch field.Type.Kind {
				case idl.TypeStruct, idl.TypeArray, idl.TypeMap:
					b.WriteString("\tin." + strings.Title(field.Name) + " = ")
					writeType(&b, field.Type, true, 1)
					b.WriteString("(" + field.Name + "_)\n")

				default:
					b.WriteString("\tin." + strings.Title(field.Name) + " = " + field.Name + "_\n")
				}
			}
			b.WriteString("\treturn c__.Send(\"" + midl.Name + "." + m.Name + "\", in, flags__)\n" +
				"}\n\n")
		} else {
			b.WriteString("\treturn c__.Send(\"" + midl.Name + "." + m.Name + "\", nil, flags__)\n" +
				"}\n\n")
		}

		b.WriteString("func Read" + m.Name + "_(c__ *varlink.Connection")
		for _, field := range m.Out.Fields {
			b.WriteString(", " + field.Name + "_ *")
			writeType(&b, field.Type, false, 1)
		}
		b.WriteString(") (bool, error) {\n")
		if len(m.Out.Fields) > 0 {
			b.WriteString("\tvar out ")
			writeType(&b, m.Out, true, 1)
			b.WriteString("\n")
			b.WriteString("\tcontinues_, err := c__.Receive(&out)\n")
		} else {
			b.WriteString("\tcontinues_, err := c__.Receive(nil)\n")
		}
		b.WriteString("\tif err != nil {\n" +
			"\t\treturn false, err\n" +
			"\t}\n")
		for _, field := range m.Out.Fields {
			b.WriteString("\tif " + field.Name + "_ != nil {\n")
			switch field.Type.Kind {
			case idl.TypeStruct, idl.TypeArray, idl.TypeMap:
				b.WriteString("\t\t*" + field.Name + "_ = ")
				writeType(&b, field.Type, false, 2)
				b.WriteString(" (out." + strings.Title(field.Name) + ")\n")

			default:
				b.WriteString("\t\t*" + field.Name + "_ = out." + strings.Title(field.Name) + "\n")
			}
			b.WriteString("\t}\n")
		}

		b.WriteString("\treturn continues_, nil\n" +
			"}\n\n")
	}

	b.WriteString("// Service interface with all methods\n")
	b.WriteString("type " + pkgname + "Interface interface {\n")
	for _, m := range midl.Methods {
		b.WriteString("\t" + m.Name + "(c__ VarlinkCall")
		for _, field := range m.In.Fields {
			b.WriteString(", " + field.Name + "_ ")
			writeType(&b, field.Type, false, 1)
		}
		b.WriteString(") error\n")
	}
	b.WriteString("}\n\n")

	b.WriteString("// Service object with all methods\n")
	b.WriteString("type VarlinkCall struct{ varlink.Call }\n\n")

	b.WriteString("// Reply methods for all varlink errors\n")
	for _, e := range midl.Errors {
		b.WriteString("func (c__ *VarlinkCall) Reply" + e.Name + "(")
		for i, field := range e.Type.Fields {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(field.Name + "_ ")
			writeType(&b, field.Type, false, 1)
		}
		b.WriteString(") error {\n")
		if len(e.Type.Fields) > 0 {
			b.WriteString("\tvar out ")
			writeType(&b, e.Type, true, 1)
			b.WriteString("\n")
			for _, field := range e.Type.Fields {
				switch field.Type.Kind {
				case idl.TypeStruct, idl.TypeArray, idl.TypeMap:
					b.WriteString("\tout." + strings.Title(field.Name) + " = ")
					writeType(&b, field.Type, true, 1)
					b.WriteString("(" + field.Name + "_)\n")

				default:
					b.WriteString("\tout." + strings.Title(field.Name) + " = " + field.Name + "_\n")
				}
			}
			b.WriteString("\treturn c__.ReplyError(\"" + midl.Name + "." + e.Name + "\", &out)\n")
		} else {
			b.WriteString("\treturn c__.ReplyError(\"" + midl.Name + "." + e.Name + "\", nil)\n")
		}
		b.WriteString("}\n\n")
	}

	b.WriteString("// Reply methods for all varlink methods\n")
	for _, m := range midl.Methods {
		b.WriteString("func (c__ *VarlinkCall) Reply" + m.Name + "(")
		for i, field := range m.Out.Fields {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(field.Name + "_ ")
			writeType(&b, field.Type, false, 1)
		}
		b.WriteString(") error {\n")
		if len(m.Out.Fields) > 0 {
			b.WriteString("\tvar out ")
			writeType(&b, m.Out, true, 1)
			b.WriteString("\n")
			for _, field := range m.Out.Fields {
				switch field.Type.Kind {
				case idl.TypeStruct, idl.TypeArray, idl.TypeMap:
					b.WriteString("\tout." + strings.Title(field.Name) + " = ")
					writeType(&b, field.Type, true, 1)
					b.WriteString("(" + field.Name + "_)\n")

				default:
					b.WriteString("\tout." + strings.Title(field.Name) + " = " + field.Name + "_\n")
				}
			}
			b.WriteString("\treturn c__.Reply(&out)\n")
		} else {
			b.WriteString("\treturn c__.Reply(nil)\n")
		}
		b.WriteString("}\n\n")
	}

	b.WriteString("// Dummy methods for all varlink methods\n")
	for _, m := range midl.Methods {
		b.WriteString("func (s__ *VarlinkInterface) " + m.Name + "(c__ VarlinkCall")
		for _, field := range m.In.Fields {
			b.WriteString(", " + field.Name + "_ ")
			writeType(&b, field.Type, false, 1)
		}
		b.WriteString(") error {\n" +
			"\treturn c__.ReplyMethodNotImplemented(\"" + m.Name + "\")\n" +
			"}\n\n")
	}

	b.WriteString("// Method call dispatcher\n")
	b.WriteString("func (s__ *VarlinkInterface) VarlinkDispatch(call varlink.Call, methodname string) error {\n" +
		"\tswitch methodname {\n")
	for _, m := range midl.Methods {
		b.WriteString("\tcase \"" + m.Name + "\":\n")
		if len(m.In.Fields) > 0 {
			b.WriteString("\t\tvar in ")
			writeType(&b, m.In, true, 2)
			b.WriteString("\n")
			b.WriteString("\t\terr := call.GetParameters(&in)\n" +
				"\t\tif err != nil {\n" +
				"\t\t\treturn call.ReplyInvalidParameter(\"parameters\")\n" +
				"\t\t}\n")
			b.WriteString("\t\treturn s__." + pkgname + "Interface." + m.Name + "(VarlinkCall{call}")
			if len(m.In.Fields) > 0 {
				for _, field := range m.In.Fields {
					switch field.Type.Kind {
					case idl.TypeStruct, idl.TypeArray, idl.TypeMap:
						b.WriteString(", ")
						writeType(&b, field.Type, false, 2)
						b.WriteString("(in." + strings.Title(field.Name) + ")")

					default:
						b.WriteString(", in." + strings.Title(field.Name))
					}
				}
			}
			b.WriteString(")\n")
		} else {
			b.WriteString("\t\treturn s__." + pkgname + "Interface." + m.Name + "(VarlinkCall{call})\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("\tdefault:\n" +
		"\t\treturn call.ReplyMethodNotFound(methodname)\n" +
		"\t}\n" +
		"}\n\n")

	b.WriteString("// Varlink interface name\n")
	b.WriteString("func (s__ *VarlinkInterface) VarlinkGetName() string {\n" +
		"\treturn `" + midl.Name + "`\n" + "}\n\n")

	b.WriteString("// Varlink interface description\n")
	b.WriteString("func (s__ *VarlinkInterface) VarlinkGetDescription() string {\n" +
		"\treturn `" + midl.Description + "\n`\n}\n\n")

	b.WriteString("// Service interface\n")
	b.WriteString("type VarlinkInterface struct {\n" +
		"\t" + pkgname + "Interface\n" +
		"}\n\n")

	b.WriteString("func VarlinkNew(m " + pkgname + "Interface) *VarlinkInterface {\n" +
		"\treturn &VarlinkInterface{m}\n" +
		"}\n")

	ret_string := b.String()

	if strings.Contains(ret_string, "json.RawMessage") {
		ret_string = strings.Replace(ret_string, "@IMPORTS@", "import (\n\t\"github.com/varlink/go/varlink\"\n\t\"encoding/json\"\n)", 1)
	} else {
		ret_string = strings.Replace(ret_string, "@IMPORTS@", `import "github.com/varlink/go/varlink"`, 1)
	}

	pretty, err := format.Source([]byte(ret_string))
	if err != nil {
		return "", nil, err
	}

	return pkgname, pretty, nil
}

func generateFile(varlinkFile string) {
	file, err := ioutil.ReadFile(varlinkFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading file '%s': %s\n", varlinkFile, err)
		os.Exit(1)
	}

	pkgname, b, err := generateTemplate(string(file))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing file '%s': %s\n", varlinkFile, err)
		os.Exit(1)
	}

	filename := path.Dir(varlinkFile) + "/" + pkgname + ".go"
	err = ioutil.WriteFile(filename, b, 0660)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error writing file '%s': %s\n", filename, err)
		os.Exit(1)
	}
}

func main() {
	if len(os.Args) != 2 {
		fmt.Printf("Usage: %s <file>\n", os.Args[0])
		os.Exit(1)
	}
	generateFile(os.Args[1])
}
