package menu

import (
	"fmt"

	"github.com/meran77777/cando1/internal/config"
)

// cloudflareSetup is a streamlined wizard for fronting the foreign server behind
// Cloudflare with the `wss` transport. It fills in every cando1-side setting
// automatically (transport, SNI/Host, WebSocket path, verification) so the only
// thing the user provides is the domain — then it prints the exact Cloudflare
// dashboard steps, which are the one part cando1 cannot do for you.
//
// Behind Cloudflare the client connects to Cloudflare's edge, which presents a
// real, publicly-trusted certificate for the domain — so the client verifies it
// (insecure = false) and the traffic is indistinguishable from ordinary HTTPS to
// a CDN. The origin's self-signed cert is only ever seen by Cloudflare.
func cloudflareSetup() error {
	fmt.Print(`
  ============  CLOUDFLARE (wss) QUICK SETUP  ============
  Fronting your foreign server behind Cloudflare makes the tunnel look like
  normal HTTPS traffic to a CDN and hides your origin IP. cando1 fills in all
  of its own settings; you only provide a domain and do a few dashboard clicks.

  Requirements:
   - A domain whose DNS is managed by Cloudflare (free plan is fine).
   - The foreign server reachable from Cloudflare on port 443.
  =======================================================
`)
	domain := askRequired("  Your domain/subdomain for the tunnel (e.g. tunnel.example.com)")
	serverIP := askDefault("  Foreign server public IP (used only in the DNS hint below)", "<foreign-server-ip>")

	tok := askDefault("  Token (blank = auto-generate)", "")
	if tok == "" {
		tok = genToken()
		fmt.Printf("  -> generated token: %s\n", tok)
	}

	// Roles: Cloudflare fronts the SERVER, so the standard topology is scenario 1
	// (client in Iran forwards chosen local ports out to the foreign server).
	d := wizardData{
		scenario:    1,
		transport:   config.TransportWSS,
		token:       tok,
		serverAddr:  domain + ":443",
		bindPort:    "443",
		sni:         domain,
		host:        domain,
		wsPath:      "/cando",
		fingerprint: "chrome",
		insecure:    false, // client sees Cloudflare's valid cert for the domain
		poolSize:    2,
	}
	if askYesNo("  Is THIS machine the FOREIGN server (the one behind Cloudflare)?", true) {
		d.thisMachine = "server"
	} else {
		d.thisMachine = "client"
	}
	d.forwards = gatherForwards()

	serverTOML := buildServerTOML(d)
	clientTOML := buildClientTOML(d)
	serverPath := "cando1-server.toml"
	clientPath := "cando1-client.toml"
	if err := writeFile(serverPath, serverTOML); err != nil {
		return err
	}
	if err := writeFile(clientPath, clientTOML); err != nil {
		return err
	}

	printCloudflareSteps(domain, serverIP)

	fmt.Printf(`
  Wrote:
    %s   -> run on the FOREIGN server (behind Cloudflare)
    %s   -> run on the IRAN client
`, serverPath, clientPath)

	// Offer to start the config for this machine in the background.
	path := clientPath
	if d.thisMachine == "server" {
		path = serverPath
	}
	if askYesNo(fmt.Sprintf("  Start the %s here in the background now?", d.thisMachine), false) {
		return startBackgroundPath(path)
	}
	return nil
}

func printCloudflareSteps(domain, serverIP string) {
	fmt.Printf(`
  ---- Cloudflare dashboard steps (do these once) ----
   1. DNS -> Records -> Add record:
        Type: A     Name: %s     IPv4: %s
        Proxy status: Proxied  (orange cloud ON)
   2. SSL/TLS -> Overview -> encryption mode: Full
        (Full lets Cloudflare accept the origin's self-signed cert.)
   3. Network -> WebSockets: On   (on by default — just confirm)
   4. Make sure the foreign server is reachable on port 443 and nothing else
      is already using it.
   5. The client dials %s:443 — no other change needed.

  Tip: keep the orange cloud ON (Proxied). A "DNS only" (grey) record exposes
  your origin IP and removes the HTTPS camouflage.
  ----------------------------------------------------
`, domain, serverIP, domain)
}
