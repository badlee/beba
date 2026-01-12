package console

import (
	"http-server/modules"
	"log"
	"os"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/util"
	"github.com/fatih/color"
	"github.com/gofiber/fiber/v3"
)

var (
	stderrLogger              = log.Default() // the default logger output to stderr
	stdoutLogger              = log.New(os.Stdout, "", log.LstdFlags)
	yellow                    = color.New(color.FgYellow).SprintFunc()
	red                       = color.New(color.FgRed).SprintFunc()
	blue                      = color.New(color.FgBlue).SprintFunc()
	green                     = color.New(color.FgGreen).SprintFunc()
	defaultStdPrinter Printer = &StdPrinter{
		StdoutPrint: func(s string) { stdoutLogger.Print(s) },
		StderrPrint: func(s string) { stderrLogger.Print(s) },
	}
)

type Printer interface {
	Log(string)
	Debug(string)
	Info(string)
	Warn(string)
	Error(string)
}

// StdPrinter implements the console.Printer interface
// that prints to the stdout or stderr.
type StdPrinter struct {
	StdoutPrint func(s string)
	StderrPrint func(s string)
}

// Debug implements [Printer].
func (p *StdPrinter) Debug(s string) {
	p.StdoutPrint("DEBUG " + s)
}

// Info implements [Printer].
func (p *StdPrinter) Info(s string) {
	p.StdoutPrint(blue("INFO") + " " + s)
}

// Log implements [Printer].
func (p *StdPrinter) Log(s string) {
	p.StdoutPrint(green("LOG") + " " + s)
}

// Warn implements [Printer].
func (p *StdPrinter) Warn(s string) {
	p.StderrPrint(yellow("WARN") + " " + s)
}

// Error implements [Printer].
func (p StdPrinter) Error(s string) {
	p.StderrPrint(red("ERROR") + " " + s)
}

type Module struct {
	util    *goja.Object
	runtime *goja.Runtime
	printer Printer
}

func (c *Module) Name() string {
	return "console"
}

func (c *Module) Doc() string {
	return "Node.js console module"
}

func (c *Module) Loader(ctx fiber.Ctx, vm *goja.Runtime, module *goja.Object) {
	cUtil := vm.NewObject()
	c.runtime = vm

	cUtil.Set("exports", vm.NewObject())
	util.Require(vm, cUtil)
	c.util = cUtil.Get("exports").(*goja.Object)
	o := module.Get("exports").(*goja.Object)
	o.Set("log", c.log(c.printer.Log))
	o.Set("error", c.log(c.printer.Error))
	o.Set("warn", c.log(c.printer.Warn))
	o.Set("info", c.log(c.printer.Info))
	o.Set("debug", c.log(c.printer.Debug))
}
func (c *Module) log(p func(string)) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		if format, ok := goja.AssertFunction(c.util.Get("format")); ok {
			ret, err := format(c.util, call.Arguments...)
			if err != nil {
				panic(err)
			}

			p(ret.String())
		} else {
			panic(c.runtime.NewTypeError("util.format is not a function"))
		}

		return nil
	}
}
func init() {
	modules.RegisterModule(&Module{
		printer: defaultStdPrinter,
	})
}
