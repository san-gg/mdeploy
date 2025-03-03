package mdeploy

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type credential struct {
	source   string
	username string
	password string
}

type steps struct {
	task        string
	param       map[string]any
	description string
}

type ymlConfig struct {
	Name       string     `yaml:"name"`
	Credential credential `yaml:"credential"`
	Steps      []steps    `yaml:"steps"`
}

func (c *credential) UnmarshalYAML(value *yaml.Node) error {
	var credential struct {
		Source   string `yaml:"source"`
		Username string `yaml:"username"`
		Password string `yaml:"password"`
	}
	if err := value.Decode(&credential); err != nil {
		return err
	}
	c.source = os.ExpandEnv(credential.Source)
	c.username = os.ExpandEnv(credential.Username)
	c.password = os.ExpandEnv(credential.Password)
	return nil
}

func (s *steps) UnmarshalYAML(value *yaml.Node) error {
	var steps struct {
		Task        string         `yaml:"task"`
		Param       map[string]any `yaml:",inline"`
		Description string         `yaml:"description,omitempty"`
	}
	if err := value.Decode(&steps); err != nil {
		return err
	}
	s.task = steps.Task
	s.description = steps.Description
	s.param = make(map[string]any)
	for k, v := range steps.Param {
		if _, ok := v.(string); ok {
			v = os.ExpandEnv(v.(string))
		}
		s.param[k] = v
	}
	return nil
}

var (
	COPYTOSERVER_TASK   = "COPYTOSERVER"
	COPYFROMSERVER_TASK = "COPYFROMSERVER"
	RUN_TASK            = "RUN"
	EXEC_TASK           = "EXEC"
	DELAY_TASK          = "DELAY"
)

func parseYml(file string) (*ymlConfig, error) {
	var yml ymlConfig
	bytes, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(bytes, &yml); err != nil {
		return nil, err
	}
	return &yml, err
}

func validateYml(yml *ymlConfig) (err error) {
outer:
	for _, s := range yml.Steps {
		switch s.task {
		case COPYTOSERVER_TASK:
			fallthrough
		case COPYFROMSERVER_TASK:
			if _, ok := s.param["source"].(string); !ok {
				err = fmt.Errorf("missing source parameter for %s task", s.task)
				break outer
			}
			if _, ok := s.param["destination"].(string); !ok {
				err = fmt.Errorf("missing destination parameter for %s task", s.task)
				break outer
			}
		case RUN_TASK:
			if _, ok := s.param["file"].(string); !ok {
				err = fmt.Errorf("missing file parameter for %s task", s.task)
				break outer
			}
		case EXEC_TASK:
			if _, ok := s.param["command"].(string); !ok {
				err = fmt.Errorf("missing command parameter for %s task", s.task)
				break outer
			}
		case DELAY_TASK:
			if _, ok := s.param["seconds"].(int); !ok {
				err = fmt.Errorf("missing seconds parameter for DELAY task")
				break outer
			}
		default:
			err = fmt.Errorf("invalid Task : %s", s.task)
			break outer
		}
	}
	return
}
