package client

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"log"
	"net"
	"time"
	// mysql
	"github.com/go-sql-driver/mysql"
	"github.com/kubernetes/dashboard/src/app/backend/resource/report"
)

var createTableStatements = []string{
	`CREATE DATABASE IF NOT EXISTS report DEFAULT CHARACTER SET = 'UTF8' DEFAULT COLLATE 'utf8_general_ci';`,
	`USE report`,
	`CREATE TABLE IF NOT EXISTS report (
      name varchar (40) NOT NULL,
      namespace varchar(40) NOT NULL,
      username varchar(40) NOT NULL,
      kind varchar(40) NOT NULL,
      resource varchar(40) NOT NULL,
      target  varchar(40) NOT NULL,
      start varchar(40) NOT NULL,
      end varchar(40) NOT NULL,
      step varchar(40) NOT NULL,
			formname varchar(40) NOT NULL,
			createtimestamp varchar(40) NOT NULL,
      PRIMARY KEY (name,namespace,username,formname)
    )`,
	`CREATE TABLE IF NOT EXISTS app (
      name varchar (40) NOT NULL,
      namespace varchar(40) NOT NULL,
      user varchar(40) NOT NULL,
			parent varchar(40) NOT NULL,
			status varchar(40) NOT NULL,
			createtimestamp varchar(40) NOT NULL,
      PRIMARY KEY (name,namespace,user,parent)
    )`,
}

/*
mysql:
name|namespace|username|kind|resource|target|start|end|step|formname|createtimestamp

appgroup:
name|namespace|user|parent|createtimestamp|status
*/

// EnSureTableExist ...
func EnSureTableExist(mysqlHost string, mysqlPwd string) error {
	success, err := net.Dial("tcp", mysqlHost)
	if err != nil {
		log.Fatal("mysql is not health in net.Dial")
	}
	success.Close()

	// connect mysql using mysqHost
	conn, err := sql.Open("mysql", fmt.Sprintf("root:%s@tcp(%s)/", mysqlPwd, mysqlHost))
	if err != nil {
		log.Fatal(err)
		return err
	}
	defer conn.Close()
	if conn.Ping() == driver.ErrBadConn {
		return fmt.Errorf("mysql: could not connect to the database. " +
			"could be bad address, or this address is not whitelisted for access.")
	}

	if _, err = conn.Exec("USE report"); err != nil {
		mErr, ok := err.(*mysql.MySQLError)
		if ok && mErr.Number == 1049 {
			fmt.Print("mysql: creating database report")
			return createTable(conn)
		}
	}

	return err
}

func createTable(connection *sql.DB) error {
	for _, stmt := range createTableStatements {
		if _, err := connection.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// CreateMySQLConn ...
func CreateMySQLConn(mysqlHost string, mysqlPwd string) (*sql.DB, error) {

	mysqlEnd := fmt.Sprintf("root:%s@tcp(%s)/report?charset=utf8&parseTime=true", mysqlPwd, mysqlHost)
	db, err := sql.Open("mysql", mysqlEnd)
	if err != nil {
		log.Fatalf("open mysql found err: %v", err)
		return nil, err
	}
	log.Printf("create mysql connection with %s", mysqlHost)
	err = db.Ping()
	if err != nil {
		log.Fatalf("db.Ping found err:%v", err)
	}
	return db, nil
}

// CreateAppGroup ...
func CreateAppGroup(db *sql.DB, rf report.AppGroup) {
	stm, _ := db.Prepare("INSERT INTO app(name,namespace,user,parent,status,createtimestamp)values(?,?,?,?,?,?)")
	defer stm.Close()

	_, err := stm.Exec(rf.Meta.Name, rf.Meta.NameSpace, rf.Meta.User, rf.Parent, rf.Status, time.Now().Format(time.RFC3339))
	if err != nil {
		log.Print(err)
	}

}

// UpdateAppGroup ...
func UpdateAppGroup(db *sql.DB, rf report.AppGroup) {
	stm, _ := db.Prepare("UPDATE app set status=? where namespace=? AND user=? AND parent=?")
	defer stm.Close()
	_, err := stm.Exec(rf.Status, rf.Meta.NameSpace, rf.Meta.User, rf.Parent)
	if err != nil {
		log.Print(err)
	}

}

// UpdateAppGroupGigAPP ...
func UpdateAppGroupGigAPP(db *sql.DB, rf report.AppGroup) {
	stm, _ := db.Prepare("UPDATE app set status=? where namespace=? AND user=? AND parent=? AND name=?")
	defer stm.Close()
	_, err := stm.Exec(rf.Status, rf.Meta.NameSpace, rf.Meta.User, rf.Parent, rf.Meta.Name)
	if err != nil {
		log.Print(err)
	}

}

// DeleteAppGroup ...
func DeleteAppGroup(db *sql.DB, rf report.AppGroup) {
	stm, err := db.Prepare("DELETE FROM app where namespace=? AND user=? AND parent=?")
	if err != nil {
		log.Printf("prepare delete  app mysql happened error which is %#v", err)
	}
	_, err = stm.Exec(rf.Meta.NameSpace, rf.Meta.User, rf.Parent)
	if err != nil {
		log.Printf("delete form from mysql happened error which is %#v", err)
	}
	defer stm.Close()
	if err != nil {
		log.Print(err)
	}
}

// ListAppGroup ... need unit test
func ListAppGroup(db *sql.DB, rf report.AppGroup) []report.AppGroup {
	var stm *sql.Stmt
	var rows *sql.Rows
	var err error
	switch {
	case (rf.Meta.NameSpace != "") && (rf.Parent != "") && (rf.Meta.User != ""):
		stm, err = db.Prepare("SELECT name,namespace,user,parent,status,createtimestamp FROM app where namespace=? AND user=? AND parent=?")
		if err != nil {
			log.Printf("stm perpare happened error which is %#v", err)
		}
		rows, err = stm.Query(rf.Meta.NameSpace, rf.Meta.User, rf.Parent)
	case rf.Meta.NameSpace != "" && rf.Meta.User != "":
		stm, err = db.Prepare("SELECT name,namespace,user,parent,status,createtimestamp FROM app where namespace=? AND user=? AND parent='/'")
		if err != nil {
			log.Printf("stm perpare happened error which is %#v", err)
		}
		rows, err = stm.Query(rf.Meta.NameSpace, rf.Meta.User)
	case rf.Meta.NameSpace != "":
		stm, err = db.Prepare("SELECT name,namespace,user,parent,status,createtimestamp FROM app where namespace=? AND parent='/'")
		if err != nil {
			log.Printf("stm perpare happened error which is %#v", err)
		}
		rows, err = stm.Query(rf.Meta.NameSpace)
	}
	list := []report.AppGroup{}
	if err != nil {
		log.Printf("GetForm: stm query happened error which is %#v", err)
		return list
	}

	defer stm.Close()
	defer rows.Close()

	for rows.Next() {
		var name, namespace, user, parent, status, createtimestamp string
		if err := rows.Scan(&name, &namespace, &user, &parent, &status, &createtimestamp); err != nil {
			log.Fatal(err)
		}
		log.Printf("list len is %#v", len(list))

		list = append(list, report.AppGroup{
			Meta: report.Meta{
				Name:      name,
				NameSpace: namespace,
				User:      user,
			},
			Parent:          parent,
			CreateTimestamp: createtimestamp,
			Status:          status,
		})

	}
	return list
}

// GetForm ...
func GetForm(db *sql.DB, rf *report.FormList) {

	stm, err := db.Prepare("SELECT formname, kind, resource, target, start, end, step, createtimestamp FROM report where name=? AND namespace=? AND username=?")
	if err != nil {
		log.Printf("GetForm: stm perpare happened error which is %#v", err)
	}
	log.Printf("GetForm: name is %#v,namespace is %s,username is %s", rf.Meta.Name, rf.Meta.NameSpace, rf.Meta.User)
	rows, err := stm.Query(rf.Meta.Name, rf.Meta.NameSpace, rf.Meta.User)

	if err != nil {
		log.Printf("GetForm: stm query happened error which is %#v", err)
	}
	defer stm.Close()
	rf.Items = make([]*report.Form, 0)

	for rows.Next() {
		rep := &report.Form{}
		rep.Range = &report.Range{}
		if err := rows.Scan(&rep.Name, &rep.Kind, &rep.Resource, &rep.Target, &rep.Range.Start, &rep.Range.End, &rep.Range.Step, &rf.CreateTimestamp); err != nil {
			log.Printf("GetForm: row scan happened error which is %#v", err)
			log.Fatal(err)
		}
		rep.Meta = rf.Meta

		rf.Items = append(rf.Items, rep)

	}
	if err := rows.Err(); err != nil {
		log.Printf("rows error is %v", err)
	}
	defer rows.Close()

}

// DeleteForm ...
func DeleteForm(db *sql.DB, rf report.FormList) {
	stm, err := db.Prepare("DELETE FROM report where name=? AND namespace=? AND username=?")
	if err != nil {
		log.Printf("prepare delete form from mysql happened error which is %#v", err)
	}
	_, err = stm.Exec(rf.Meta.Name, rf.Meta.NameSpace, rf.Meta.User)
	if err != nil {
		log.Printf("delete form from mysql happened error which is %#v", err)
	}
	defer stm.Close()
	if err != nil {
		log.Fatal(err)
	}
}

// UpdateForm ...
func UpdateForm(db *sql.DB, rf *report.Form) {
	stm, _ := db.Prepare("UPDATE report set kind=?,resource=?,target=?,start=?,end=?,step=? where name=? AND namespace=? AND username=? AND formname=?")
	defer stm.Close()
	_, err := stm.Exec(&rf.Kind, &rf.Resource, &rf.Target, &rf.Range.Start, &rf.Range.End, &rf.Range.Step, &rf.Meta.Name, &rf.Meta.NameSpace, &rf.Meta.User, &rf.Name)
	if err != nil {
		log.Fatal(err)
	}

}

// CreateForm ...
func CreateForm(db *sql.DB, rf *report.FormList) {
	stm, _ := db.Prepare("INSERT INTO report(name,namespace,username,kind,resource,target,start,end,step,formname,createtimestamp)values(?,?,?,?,?,?,?,?,?,?,?)")
	defer stm.Close()
	if len(rf.Items) > 0 {
		for _, item := range rf.Items {
			_, err := stm.Exec(rf.Meta.Name, rf.Meta.NameSpace, rf.Meta.User, item.Kind, item.Resource, item.Target, item.Range.Start, item.Range.End, item.Range.Step, item.Meta.Name, rf.CreateTimestamp)
			if err != nil {
				log.Fatal(err)
			}
		}
	} else {
		_, err := stm.Exec(rf.Meta.Name, rf.Meta.NameSpace, rf.Meta.User, "", "", "", "", "", "", "default", time.Now().Format(time.RFC3339))
		if err != nil {
			log.Fatal(err)
		}
	}

}

// CreateFormSig ...
func CreateFormSig(db *sql.DB, rf *report.Form) {
	stm, _ := db.Prepare("INSERT INTO report(name,namespace,username,kind,resource,target,start,end,step,formname,createtimestamp)values(?,?,?,?,?,?,?,?,?,?,?)")
	defer stm.Close()
	log.Print(rf)
	_, err := stm.Exec(rf.Meta.Name, rf.Meta.NameSpace, rf.Meta.User, rf.Kind, rf.Resource, rf.Target, rf.Range.Start, rf.Range.End, rf.Range.Step, rf.Name, time.Now().Format(time.RFC3339))
	if err != nil {
		log.Print(err)
	}

}

// ListForm ... need unit test
func ListForm(db *sql.DB, rf report.Meta) []report.Info {
	stm, err := db.Prepare("SELECT name,createtimestamp FROM report where namespace=? AND username=?")
	if err != nil {
		log.Printf("stm perpare happened error which is %#v", err)
	}
	fmt.Printf("usernae is %#v, namespace is %#v", rf.User, rf.NameSpace)
	rows, _ := stm.Query(rf.NameSpace, rf.User)
	if err != nil {
		log.Printf("sql query happened error which  is %#v", err)
	}
	defer stm.Close()
	defer rows.Close()
	list := []report.Info{}

	for rows.Next() {
		var name, createtimestamp string
		var set bool = true
		if err := rows.Scan(&name, &createtimestamp); err != nil {
			log.Fatal(err)
		}
		log.Printf("list len is %#v", len(list))
		if len(list) == 0 {
			list = append(list, report.Info{Name: name, CreateTimestamp: createtimestamp})
		}

		for i := 0; i < len(list); i++ {

			if list[i].Name == name {
				set = false
			}

		}
		if set == true {
			list = append(list, report.Info{Name: name, CreateTimestamp: createtimestamp})
		}

		// fmt.Printf("name:%s ,id:is %d\n", name, id)
	}
	return list
}

// DeleteFormSig ...
func DeleteFormSig(db *sql.DB, rf report.Form) {
	stm, err := db.Prepare("DELETE FROM report where name=? AND namespace=? AND username=? AND formname=?")
	if err != nil {
		log.Printf("prepare delete form from mysql happened error which is %#v", err)
	}
	_, err = stm.Exec(rf.Meta.Name, rf.Meta.NameSpace, rf.Meta.User, rf.Name)
	if err != nil {
		log.Printf("delete form from mysql happened error which is %#v", err)
	}
	defer stm.Close()
	if err != nil {
		log.Fatal(err)
	}
}
