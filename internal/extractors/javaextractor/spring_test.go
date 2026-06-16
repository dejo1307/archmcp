package javaextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func TestSpring_Routes(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"web/EdqsController.java": `package web;

import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/api/edqs")
public class EdqsController {

    @GetMapping("/ready")
    public void isReady() {}

    @PostMapping("/reset")
    public void reset() {}
}
`,
	})

	routes := factsByKind(ff, facts.KindRoute)
	if len(routes) != 2 {
		t.Fatalf("want 2 routes, got %d: %v", len(routes), names(ff))
	}

	want := map[string]string{
		"/api/edqs/ready": "GET",
		"/api/edqs/reset": "POST",
	}
	for _, r := range routes {
		method, ok := want[r.Name]
		if !ok {
			t.Errorf("unexpected route %q", r.Name)
			continue
		}
		if r.Props["method"] != method {
			t.Errorf("route %q method = %v, want %v", r.Name, r.Props["method"], method)
		}
		if r.Props["framework"] != "spring" {
			t.Errorf("route %q framework = %v", r.Name, r.Props["framework"])
		}
		if r.Props["handler"] == "" {
			t.Errorf("route %q missing handler", r.Name)
		}
	}
}

func TestSpring_RequestMappingWithMethod(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"web/UserController.java": `package web;

import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestMethod;
import org.springframework.web.bind.annotation.RestController;

@RestController
public class UserController {

    @RequestMapping(value = "/users", method = RequestMethod.GET)
    public void list() {}
}
`,
	})
	r, ok := findFactKind(ff, facts.KindRoute, "/users")
	if !ok {
		t.Fatalf("missing /users route; got %v", names(ff))
	}
	if r.Props["method"] != "GET" {
		t.Errorf("method = %v, want GET", r.Props["method"])
	}
}

func TestSpring_Components(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"svc/UserService.java": `package svc;

import org.springframework.stereotype.Service;

@Service
public class UserService {}
`,
	})
	s, _ := findFact(ff, "svc.UserService")
	if s.Props["framework"] != "spring" || s.Props["component"] != "service" {
		t.Errorf("UserService component props = %+v", s.Props)
	}
}

func TestSpring_JpaEntity(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"model/User.java": `package model;

import javax.persistence.Entity;
import javax.persistence.Table;

@Entity
@Table(name = "users")
public class User {}
`,
	})
	st, ok := findFactKind(ff, facts.KindStorage, "model.User")
	if !ok {
		t.Fatalf("missing storage fact for @Entity; got %v", names(ff))
	}
	if st.Props["storage_kind"] != "entity" {
		t.Errorf("storage_kind = %v, want entity", st.Props["storage_kind"])
	}
	if st.Props["table"] != "users" {
		t.Errorf("table = %v, want users", st.Props["table"])
	}
}

func TestSpring_JpaTableConstantResolved(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"model/ModelConstants.java": `package model;

public class ModelConstants {
    public static final String ADMIN_SETTINGS_TABLE_NAME = "admin_settings";
}
`,
		"model/AdminSettingsEntity.java": `package model;

import javax.persistence.Entity;
import javax.persistence.Table;
import static model.ModelConstants.ADMIN_SETTINGS_TABLE_NAME;

@Entity
@Table(name = ADMIN_SETTINGS_TABLE_NAME)
public class AdminSettingsEntity {}
`,
	})
	st, ok := findFactKind(ff, facts.KindStorage, "model.AdminSettingsEntity")
	if !ok {
		t.Fatalf("missing storage fact; got %v", names(ff))
	}
	if st.Props["table"] != "admin_settings" {
		t.Errorf("table = %v, want admin_settings (resolved from constant)", st.Props["table"])
	}
	if st.Props["table_constant"] != "ADMIN_SETTINGS_TABLE_NAME" {
		t.Errorf("table_constant = %v, want ADMIN_SETTINGS_TABLE_NAME", st.Props["table_constant"])
	}
}

func TestSpring_DataRepository(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"repo/UserRepository.java": `package repo;

import org.springframework.data.jpa.repository.JpaRepository;

public interface UserRepository extends JpaRepository<User, Long> {}
`,
	})
	s, _ := findFact(ff, "repo.UserRepository")
	if s.Props["component"] != "repository" {
		t.Errorf("UserRepository component = %v, want repository", s.Props["component"])
	}
}

func TestSpring_ConstructorInjection(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"svc/Repo.java": "package svc;\npublic class Repo {}\n",
		"svc/Handler.java": `package svc;

import org.springframework.stereotype.Service;

@Service
public class Handler {
    private final Repo repo;

    public Handler(Repo repo) {
        this.repo = repo;
    }
}
`,
	})
	h, _ := findFact(ff, "svc.Handler")
	if !hasRelation(h, facts.RelInjects, "svc.Repo") {
		t.Errorf("Handler should inject svc.Repo; got %+v", h.Relations)
	}
}

func TestSpring_FieldInjection(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"svc/Dep.java": "package svc;\npublic class Dep {}\n",
		"svc/Svc.java": `package svc;

import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.stereotype.Service;

@Service
public class Svc {
    @Autowired
    private Dep dep;
}
`,
	})
	s, _ := findFact(ff, "svc.Svc")
	if !hasRelation(s, facts.RelInjects, "svc.Dep") {
		t.Errorf("Svc should inject svc.Dep via @Autowired field; got %+v", s.Relations)
	}
}

func TestSpring_LombokRequiredArgsConstructor(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"svc/Store.java": "package svc;\npublic class Store {}\n",
		"svc/Worker.java": `package svc;

import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;

@Service
@RequiredArgsConstructor
public class Worker {
    private final Store store;
}
`,
	})
	w, _ := findFact(ff, "svc.Worker")
	if !hasRelation(w, facts.RelInjects, "svc.Store") {
		t.Errorf("Worker should inject svc.Store via @RequiredArgsConstructor; got %+v", w.Relations)
	}
}

func TestDubbo_SPI(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"ext/Protocol.java": `package ext;

import org.apache.dubbo.common.extension.SPI;

@SPI
public interface Protocol {}
`,
		"ext/DubboProtocol.java": `package ext;

import org.apache.dubbo.common.extension.Activate;

@Activate
public class DubboProtocol implements Protocol {}
`,
	})
	spi, _ := findFact(ff, "ext.Protocol")
	if spi.Props["framework"] != "dubbo" || spi.Props["dubbo_spi"] != true {
		t.Errorf("Protocol dubbo props = %+v", spi.Props)
	}
	act, _ := findFact(ff, "ext.DubboProtocol")
	if act.Props["dubbo_activate"] != true {
		t.Errorf("DubboProtocol should be marked dubbo_activate; got %+v", act.Props)
	}
}
