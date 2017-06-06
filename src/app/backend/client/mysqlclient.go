package client

import (
	"fmt"
	"log"
	"net"

	"database/sql"
	"database/sql/driver"
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
      PRIMARY KEY (name,namespace,username)
    )`,
}

/*
mysql:
name|namespace|username|kind|resource|target|start|end|step
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

// GetForm ...
func GetForm(db *sql.DB, rf *report.Form) {

	stm, err := db.Prepare("SELECT kind, resource, target, start, end, step FROM report where name=? AND namespace=? AND username=?")
	if err != nil {
		log.Printf("GetForm: stm perpare happened error which is %#v", err)
	}
	log.Printf("GetForm: name is %#v,namespace is %s,username is %s", rf.Meta.Name, rf.Meta.NameSpace, rf.Meta.User)
	rows, err := stm.Query(rf.Meta.Name, rf.Meta.NameSpace, rf.Meta.User)

	if err != nil {
		log.Printf("GetForm: stm query happened error which is %#v", err)
	}
	defer stm.Close()

	for rows.Next() {
		log.Printf("report is %#v", rf)
		rf.Range = &report.Range{}
		if err := rows.Scan(&rf.Kind, &rf.Resource, &rf.Target, &rf.Range.Start, &rf.Range.End, &rf.Range.Step); err != nil {
			log.Printf("GetForm: row scan happened error which is %#v", err)
			log.Fatal(err)
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf("rows error is %v", err)
	}
	defer rows.Close()

}

// DeleteForm ...
func DeleteForm(db *sql.DB, rf report.Form) {
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
	stm, _ := db.Prepare("UPDATE report set kind=?,resource=?,target=?,start=?,end=?,step=? where name=? AND namespace=? AND username=?")
	defer stm.Close()
	_, err := stm.Exec(&rf.Kind, &rf.Resource, &rf.Target, &rf.Range.Start, &rf.Range.End, &rf.Range.Step, &rf.Meta.Name, &rf.Meta.NameSpace, &rf.Meta.User)
	if err != nil {
		log.Fatal(err)
	}

}

// CreateForm ...
func CreateForm(db *sql.DB, rf *report.Form) {
	stm, _ := db.Prepare("INSERT INTO report(name,namespace,username,kind,resource,target,start,end,step)values(?,?,?,?,?,?,?,?,?)")
	defer stm.Close()
	_, err := stm.Exec(rf.Meta.Name, rf.Meta.NameSpace, rf.Meta.User, rf.Kind, rf.Resource, rf.Target, rf.Range.Start, rf.Range.End, rf.Range.Step)
	if err != nil {
		log.Fatal(err)
	}
}

// ListForm ... need unit test
func ListForm(db *sql.DB, rf *report.Form) []string {
	stm, err := db.Prepare("SELECT name FROM report where namespace=? AND username=?")
	if err != nil {
		log.Printf("stm perpare happened error which is %#v", err)
	}
	fmt.Printf("usernae is %#v, namespace is %#v", rf.Meta.User, rf.Meta.NameSpace)
	rows, _ := stm.Query(rf.Meta.NameSpace, rf.Meta.User)
	if err != nil {
		log.Printf("sql query happened error which  is %#v", err)
	}
	defer stm.Close()
	defer rows.Close()
	list := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			log.Fatal(err)
		}
		list = append(list, name)
		// fmt.Printf("name:%s ,id:is %d\n", name, id)
	}
	return list
}
