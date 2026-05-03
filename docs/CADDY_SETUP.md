# Caddy Setup Guide for DV Hub Gateway

## Why Caddy?

**Advantages over nginx:**
- Automatic HTTPS via Let's Encrypt (zero config)
- Simpler configuration syntax
- Built-in WebSocket support
- Automatic certificate renewal
- No need for certbot/acme.sh

## Installation

### Ubuntu/Debian
```bash
sudo apt install -y debian-keyring debian-archive-keyring apt-transport-https
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | sudo tee /etc/apt/sources.list.d/caddy-stable.list
sudo apt update
sudo apt install caddy
```

### RHEL/CentOS/Fedora
```bash
dnf install 'dnf-command(copr)'
dnf copr enable @caddy/caddy
dnf install caddy
```

### Manual Install
```bash
# Download latest
curl -o caddy.tar.gz https://caddyserver.com/api/download?os=linux&arch=amd64
tar -xzf caddy.tar.gz caddy
sudo mv caddy /usr/bin/
sudo chmod +x /usr/bin/caddy

# Create systemd service (if not exists)
sudo caddy install
```

## Configuration

### 1. Copy Caddyfile
```bash
sudo cp Caddyfile /etc/caddy/Caddyfile
```

### 2. Edit Domain
```bash
sudo nano /etc/caddy/Caddyfile
# Change: dvhub.yourdomain.com → your-actual-domain.com
```

### 3. Validate Config
```bash
caddy validate --config /etc/caddy/Caddyfile
```

### 4. Start Caddy
```bash
sudo systemctl enable caddy
sudo systemctl start caddy
```

### 5. Check Status
```bash
sudo systemctl status caddy
sudo journalctl -u caddy -f
```

## DNS Configuration

**Before starting Caddy, ensure DNS is configured:**

```
A Record: dvhub.yourdomain.com → your-server-ip
```

**Wait for DNS propagation:**
```bash
dig dvhub.yourdomain.com +short
# Should return your server IP
```

## Firewall Rules

```bash
# UFW
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp

# firewalld
sudo firewall-cmd --permanent --add-service=http
sudo firewall-cmd --permanent --add-service=https
sudo firewall-cmd --reload

# iptables
sudo iptables -A INPUT -p tcp --dport 80 -j ACCEPT
sudo iptables -A INPUT -p tcp --dport 443 -j ACCEPT
sudo iptables-save > /etc/iptables/rules.v4
```

## Caddyfile Examples

### Development (HTTP only)
```caddy
:8080 {
    root * /var/www/dvhub
    file_server
    
    @websocket {
        path /ws
    }
    reverse_proxy @websocket localhost:8080
    reverse_proxy /api/* localhost:8080
}
```

### Production (Auto HTTPS)
```caddy
dvhub.example.com {
    root * /var/www/dvhub
    file_server
    
    @websocket {
        path /ws
    }
    reverse_proxy @websocket localhost:8080 {
        header_up Upgrade {http.request.header.Upgrade}
        header_up Connection {http.request.header.Connection}
        flush_interval -1
    }
    
    reverse_proxy /api/* localhost:8080
}
```

### Multiple Domains
```caddy
dvhub.example.com, dvhub2.example.com {
    root * /var/www/dvhub
    file_server
    
    @websocket {
        path /ws
    }
    reverse_proxy @websocket localhost:8080 {
        flush_interval -1
    }
    
    reverse_proxy /api/* localhost:8080
}
```

### Custom TLS Certificates
```caddy
dvhub.example.com {
    tls /path/to/cert.pem /path/to/key.pem
    
    root * /var/www/dvhub
    file_server
    
    # ... rest of config
}
```

### IP Whitelisting
```caddy
dvhub.example.com {
    @allowed {
        remote_ip 192.168.1.0/24 10.0.0.0/8
    }
    
    handle @allowed {
        root * /var/www/dvhub
        file_server
        
        @websocket {
            path /ws
        }
        reverse_proxy @websocket localhost:8080
    }
    
    handle {
        respond "Access Denied" 403
    }
}
```

### Basic Authentication
```caddy
dvhub.example.com {
    basicauth /admin/* {
        admin $2a$14$Zkx19XLiW6VYouLHR5NmfOFU0z2GTNmpkT/5qqR7hx16HQwA57k6
    }
    
    root * /var/www/dvhub
    file_server
    
    # ... rest of config
}

# Generate password hash:
# caddy hash-password
```

## Troubleshooting

### Check if Caddy is running
```bash
sudo systemctl status caddy
curl -I http://localhost
```

### View logs
```bash
# Real-time logs
sudo journalctl -u caddy -f

# All Caddy logs
sudo journalctl -u caddy --no-pager

# Access logs (if configured)
sudo tail -f /var/log/caddy/dvhub-access.log
```

### Test configuration
```bash
caddy validate --config /etc/caddy/Caddyfile
```

### Reload after config changes
```bash
sudo systemctl reload caddy
# or
sudo caddy reload --config /etc/caddy/Caddyfile
```

### Certificate issues
```bash
# Check certificate status
curl -vI https://dvhub.yourdomain.com

# Force certificate renewal
sudo caddy reload --force
```

### WebSocket not working
```bash
# Test WebSocket connection
wscat -c ws://localhost:8080/ws

# Check if upgrade headers are present
curl -i -N \
  -H "Connection: Upgrade" \
  -H "Upgrade: websocket" \
  http://localhost:8080/ws
```

### Port conflicts
```bash
# Check what's using port 80/443
sudo netstat -tlnp | grep :80
sudo netstat -tlnp | grep :443

# Or with ss
sudo ss -tlnp | grep :80
```

## Performance Tuning

### Increase file descriptor limits
```bash
# Edit Caddy service
sudo systemctl edit caddy

# Add:
[Service]
LimitNOFILE=65536
```

### Enable HTTP/2
```caddy
dvhub.example.com {
    protocols h1 h2 h2c
    
    # ... rest of config
}
```

### Enable compression
```caddy
dvhub.example.com {
    encode gzip zstd
    
    # ... rest of config
}
```

### Cache static files
```caddy
dvhub.example.com {
    @static {
        file
        path *.css *.js *.png *.jpg *.svg *.woff *.woff2
    }
    
    header @static {
        Cache-Control "public, max-age=31536000"
    }
    
    # ... rest of config
}
```

## Migration from nginx

### Compare configurations

**nginx:**
```nginx
location /ws {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
}
```

**Caddy:**
```caddy
@websocket {
    path /ws
}
reverse_proxy @websocket localhost:8080
```

**Advantage:** Caddy handles WebSocket upgrade automatically.

## Security Hardening

### Complete Production Config
```caddy
dvhub.example.com {
    # Security headers
    header {
        Strict-Transport-Security "max-age=31536000; includeSubDomains; preload"
        X-Frame-Options "DENY"
        X-Content-Type-Options "nosniff"
        X-XSS-Protection "1; mode=block"
        Referrer-Policy "strict-origin-when-cross-origin"
        Permissions-Policy "geolocation=(), microphone=(self), camera=()"
        -Server
    }
    
    # Logging
    log {
        output file /var/log/caddy/dvhub-access.log {
            roll_size 100mb
            roll_keep 5
            roll_keep_for 720h
        }
        format json
    }
    
    # Rate limiting (requires caddy-ratelimit plugin)
    # rate_limit {
    #     zone dynamic {
    #         key {remote_host}
    #         events 100
    #         window 1m
    #     }
    # }
    
    # Root directory
    root * /var/www/dvhub
    file_server
    
    # WebSocket
    @websocket {
        path /ws
    }
    reverse_proxy @websocket localhost:8080 {
        header_up Upgrade {http.request.header.Upgrade}
        header_up Connection {http.request.header.Connection}
        header_up X-Real-IP {http.request.remote.host}
        header_up X-Forwarded-For {http.request.remote.host}
        header_up X-Forwarded-Proto {http.request.scheme}
        flush_interval -1
    }
    
    # API
    reverse_proxy /api/* localhost:8080 {
        header_up X-Real-IP {http.request.remote.host}
        header_up X-Forwarded-For {http.request.remote.host}
    }
}
```

## Quick Reference

| Task | Command |
|------|---------|
| Install | `sudo apt install caddy` |
| Start | `sudo systemctl start caddy` |
| Stop | `sudo systemctl stop caddy` |
| Restart | `sudo systemctl restart caddy` |
| Reload | `sudo systemctl reload caddy` |
| Status | `sudo systemctl status caddy` |
| Logs | `sudo journalctl -u caddy -f` |
| Validate | `caddy validate --config /etc/caddy/Caddyfile` |
| Format | `caddy fmt /etc/caddy/Caddyfile --overwrite` |

## Resources

- **Official Docs:** https://caddyserver.com/docs/
- **Caddyfile Syntax:** https://caddyserver.com/docs/caddyfile
- **Reverse Proxy:** https://caddyserver.com/docs/caddyfile/directives/reverse_proxy
- **Community Forum:** https://caddy.community/

---

**Caddy handles TLS certificates automatically - just set your domain name and it works!**
