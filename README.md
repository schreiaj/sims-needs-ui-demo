# Halloween Sims Web Server

A simple Echo v4 web server that serves a Halloween-themed Sims-like needs management interface.

## Features

- **Session Management**: Each user gets a unique UUID-based session when visiting the root URL
- **Dynamic Routing**: 
  - `/` - Redirects to a unique session URL `/[uuid]`
  - `/[uuid]` - Displays the main needs interface (`index.html`)
  - `/[uuid]/control` - Displays the control panel (`control.html`)
- **Static File Serving**: Serves CSS and HTML files
- **In-Memory Sessions**: Session storage (no persistence yet)

## Getting Started

1. **Install Dependencies**:
   ```bash
   go mod tidy
   ```

2. **Run the Server**:
   ```bash
   go run main.go
   ```

3. **Access the Application**:
   - Open your browser to `http://localhost:8080`
   - You'll be automatically redirected to a unique session URL
   - Add `/control` to the URL to access the control panel


## API Endpoints

- `GET /` - Creates a new session and redirects to `/[uuid]`
- `GET /[uuid]` - Serves the main interface
- `GET /[uuid]/control` - Serves the control panel

