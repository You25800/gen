package gen

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"gorm.io/gorm"

	"golang.org/x/tools/imports"
	"gorm.io/gen/internal/check"
	"gorm.io/gen/internal/parser"
	tmpl "gorm.io/gen/internal/template"
	"gorm.io/gen/log"
)

// TODO implement some unit tests

// T genric type
type T interface{}

// NewGenerator create a new generator
func NewGenerator(cfg Config) *Generator {
	if cfg.modelPkgName == "" {
		cfg.modelPkgName = check.ModelPkg
	}
	return &Generator{
		Config:           cfg,
		Data:             make(map[string]*genInfo),
		readInterfaceSet: new(parser.InterfaceSet),
	}
}

// Config generator's basic configuration
type Config struct {
	OutPath string
	OutFile string

	pkgName      string
	modelPkgName string   //default model
	db           *gorm.DB //nolint
}

func (c *Config) SetModelPkg(name string) {
	c.modelPkgName = name
}

func (c *Config) SetPkg(name string) {
	c.pkgName = name
}

func (c *Config) GetPkg() string {
	return c.pkgName
}

// genInfo info about generated code
type genInfo struct {
	*check.BaseStruct
	Interfaces []*check.InterfaceMethod
}

// Generator code generator
type Generator struct {
	Config

	Data             map[string]*genInfo
	readInterfaceSet *parser.InterfaceSet
}

// UseDB set db connection
func (g *Generator) UseDB(db *gorm.DB) {
	g.db = db
}

// Tables collect table model
func (g *Generator) Tables(models ...interface{}) {
	structs, err := check.CheckStructs(g.db, models...)
	if err != nil {
		log.Fatalf("gen struct error: %s", err)
	}
	for _, interfaceStruct := range structs {
		data := g.getData(interfaceStruct.NewStructName)
		if data.BaseStruct == nil {
			data.BaseStruct = interfaceStruct
		}
	}
}

// TableNames collect table names
func (g *Generator) TableNames(names ...string) {
	structs, err := check.GenBaseStructs(g.db, g.Config.modelPkgName, names...)
	if err != nil {
		log.Fatalf("check struct error: %s", err)
	}
	for _, interfaceStruct := range structs {
		data := g.getData(interfaceStruct.NewStructName)
		if data.BaseStruct == nil {
			data.BaseStruct = interfaceStruct
		}
	}
}

// Apply specifies method interfaces on structures, implment codes will be generated after calling g.Execute()
// eg: g.Apply(func(model.Method){}, model.User{}, model.Company{})
func (g *Generator) Apply(fc interface{}, models ...interface{}) {
	var err error

	structs, err := check.CheckStructs(g.db, models...)
	if err != nil {
		log.Fatalf("check struct error: %s", err)
	}
	g.apply(fc, structs)
}

func (g *Generator) apply(fc interface{}, structs []*check.BaseStruct) {
	interfacePaths, err := parser.GetInterfacePath(fc)
	if err != nil {
		log.Fatalf("can't get interface name or file: %s", err)
	}

	err = g.readInterfaceSet.ParseFile(interfacePaths)
	if err != nil {
		log.Fatalf("parser file error: %s", err)
	}

	for _, interfaceStruct := range structs {
		data := g.getData(interfaceStruct.NewStructName)
		if data.BaseStruct == nil {
			data.BaseStruct = interfaceStruct
		}

		functions, err := check.CheckInterface(g.readInterfaceSet, interfaceStruct)
		if err != nil {
			log.Fatalf("check interface error: %s", err)
		}

		for _, function := range functions {
			data.Interfaces = function.DupAppend(data.Interfaces)
		}
	}
}

// ApplyByModel specifies one method interface on several model structures
// eg: g.ApplyByModel(model.User{}, func(model.Method1, model.Method2){})
func (g *Generator) ApplyByModel(model interface{}, fc interface{}) {
	g.Apply(fc, model)
}

// ApplyByTable specifies table by table names
// eg: g.ApplyByTable(func(model.Model){}, "user", "role")
func (g *Generator) ApplyByTable(fc interface{}, tableNames ...string) {
	structs, err := check.GenBaseStructs(g.db, g.Config.modelPkgName, tableNames...)
	if err != nil {
		log.Fatalf("gen struct error: %s", err)
	}
	g.apply(fc, structs)
}

// Execute generate code to output path
func (g *Generator) Execute() {
	var err error
	if g.OutPath == "" {
		g.OutPath = "./query"
	}
	if g.OutFile == "" {
		g.OutFile = g.OutPath + "/gorm_generated.go"
	}
	if _, err := os.Stat(g.OutPath); err != nil {
		if err := os.Mkdir(g.OutPath, os.ModePerm); err != nil {
			log.Fatalf("mkdir failed: %s", err)
		}
	}
	g.SetPkg(filepath.Base(g.OutPath))

	err = g.generatedBaseStruct()
	if err != nil {
		log.Fatalf("generate base struct fail: %s", err)
	}
	err = g.generatedToOutFile()
	if err != nil {
		log.Fatalf("generate to file fail: %s", err)
	}
	log.Println("Gorm generated interface file successful!")
	log.Println("Generated path：", g.OutPath)
	log.Println("Generated file：", g.OutFile)
}

// generatedToOutFile save generate code to file
func (g *Generator) generatedToOutFile() (err error) {
	var buf bytes.Buffer

	render := func(tmpl string, wr io.Writer, data interface{}) error {
		t, err := template.New(tmpl).Parse(tmpl)
		if err != nil {
			return err
		}
		return t.Execute(wr, data)
	}

	err = render(tmpl.HeaderTmpl, &buf, g.GetPkg())
	if err != nil {
		return err
	}

	for _, data := range g.Data {
		err = render(tmpl.BaseStruct, &buf, data.BaseStruct)
		if err != nil {
			return err
		}

		for _, method := range data.Interfaces {
			err = render(tmpl.FuncTmpl, &buf, method)
			if err != nil {
				return err
			}
		}

		err = render(tmpl.BaseGormFunc, &buf, data.BaseStruct)
		if err != nil {
			return err
		}
	}

	err = render(tmpl.UseTmpl, &buf, g)
	if err != nil {
		return err
	}

	result, err := imports.Process(g.OutFile, buf.Bytes(), nil)
	if err != nil {
		errLine, _ := strconv.Atoi(strings.Split(err.Error(), ":")[1])
		line := strings.Split(buf.String(), "\n")
		for i := -3; i < 3; i++ {
			fmt.Println(i+errLine, line[i+errLine])
		}
		return fmt.Errorf("can't format generated file: %w", err)
	}
	return outputFile(g.OutFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, result)
}

// generatedBaseStruct generate basic structures
func (g *Generator) generatedBaseStruct() (err error) {
	outPath, err := filepath.Abs(g.OutPath)
	if err != nil {
		return err
	}
	pkg := g.modelPkgName
	if pkg == "" {
		pkg = check.ModelPkg
	}
	outPath = fmt.Sprint(filepath.Dir(outPath), "/", pkg, "/")
	if _, err := os.Stat(outPath); err != nil {
		if err := os.Mkdir(outPath, os.ModePerm); err != nil {
			log.Fatalf("mkdir failed: %s", err)
		}
	}
	for _, data := range g.Data {
		if data.BaseStruct == nil || !data.BaseStruct.GenBaseStruct {
			continue
		}
		var buf bytes.Buffer
		err = render(tmpl.ModelTemplate, &buf, data.BaseStruct)
		if err != nil {
			return err
		}
		modelFile := fmt.Sprint(outPath, data.BaseStruct.TableName, ".go")
		result, err := imports.Process(modelFile, buf.Bytes(), nil)
		if err != nil {
			for i, line := range strings.Split(buf.String(), "\n") {
				fmt.Println(i, line)
			}
			return fmt.Errorf("can't format generated file: %w", err)
		}
		err = outputFile(modelFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, result)
		if err != nil {
			return nil
		}
	}
	return nil
}

func (g *Generator) getData(structName string) *genInfo {
	if g.Data[structName] == nil {
		g.Data[structName] = new(genInfo)
	}
	return g.Data[structName]
}

func outputFile(filename string, flag int, data []byte) error {
	out, err := os.OpenFile(filename, flag, 0640)
	if err != nil {
		return fmt.Errorf("can't open out file: %w", err)
	}
	return output(out, data)
}

func output(wr io.WriteCloser, data []byte) (err error) {
	defer func() {
		if e := wr.Close(); e != nil {
			err = fmt.Errorf("can't close: %w", e)
		}
	}()

	if _, err = wr.Write(data); err != nil {
		return fmt.Errorf("can't write: %w", err)
	}
	return nil
}

func render(tmpl string, wr io.Writer, data interface{}) error {
	t, err := template.New(tmpl).Parse(tmpl)
	if err != nil {
		return err
	}
	return t.Execute(wr, data)
}
