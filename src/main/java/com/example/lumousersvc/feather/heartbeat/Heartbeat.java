package com.example.lumousersvc.feather.heartbeat;

import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;

@RestController
public class Heartbeat {

    @GetMapping("/")
    public ResponseEntity<Void> heartbeatHome() {
        return ResponseEntity.ok().build();
    }

    @GetMapping("/heartbeat")
    public ResponseEntity<Void> heartbeat() {
        return ResponseEntity.ok().build();
    }
}
