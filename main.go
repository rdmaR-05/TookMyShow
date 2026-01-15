package main

import (
    "fmt"
    "net/http"
)

func main() {
    // This function handles the request
    http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
        // It writes a simple response back to the user
        fmt.Fprintf(w, "TookMyShow Backend is Alive!")
    })

    // This prints to your terminal so you know it started
    fmt.Println("Server starting on port 8080...")

    // This starts the server
    if err := http.ListenAndServe(":8080", nil); err != nil {
        fmt.Println("Error starting server:", err)
    }
}