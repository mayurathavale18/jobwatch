package web

import "testing"

func TestParseLinkedInConnectionsCSV(t *testing.T) {
	data := []byte("Notes:\r\n\"When exporting your connection data, you may notice that some of the email addresses are missing.\"\r\n\r\n" +
		"First Name,Last Name,URL,Email Address,Company,Position,Connected On\r\n" +
		"Asha,Rao,https://www.linkedin.com/in/asha,asha@example.com,\"Google, India\",Software Engineer,01 Jan 2026\r\n" +
		"Ravi,Kumar,https://www.linkedin.com/in/ravi,,Stripe,Recruiter,02 Jan 2026\r\n" +
		",,,,,,\r\n")

	conns, err := parseLinkedInConnectionsCSV(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(conns) != 2 {
		t.Fatalf("got %d connections, want 2 (blank row skipped)", len(conns))
	}
	if conns[0].Company != "Google, India" || conns[0].Email != "asha@example.com" {
		t.Errorf("first row wrong: %+v", conns[0])
	}
	if conns[1].Email != "" || conns[1].Position != "Recruiter" {
		t.Errorf("second row wrong: %+v", conns[1])
	}
}

func TestParseLinkedInConnectionsCSVRejectsOtherFiles(t *testing.T) {
	conns, err := parseLinkedInConnectionsCSV([]byte("a,b,c\n1,2,3\n"))
	if err != nil || len(conns) != 0 {
		t.Fatalf("want no connections and no error, got %d, %v", len(conns), err)
	}
}
