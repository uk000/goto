# HTTP Chaos

### Goal:
Create chaos by sending some invalid HTTP response.

### Challenge
Any self-respecting HTTP library won't let you send garbage that violates HTTP protocol.

### Can Goto help?
Sure. Here's how `Goto` can help.

1. Take a brand new shiny, sealed new in box, Goto (or reuse one).
 
2. Add a TCP listener via startup args --ports 8080,8081/tcp or via [APIs](../README.md#-listeners)

3. In `Goto`, any listener can be reconfigured on the fly via [APIs](../README.md#-tcp-server). A TCP listener port can be configured to send any arbitrary content (among other things you can do with TCP listeners in goto). We’ll configure this port 8081 to respond with HTTP response as text. Note that Goto allows you to configure one port via another port. Below we’re configuring 8081 via 8080

    ```json
    curl -s goto.goto:8080/tcp/8081/configure --data '{"payload": true, "responsePayloads": [ "HTTP/1.1 200 OK\r\nGoto: goto\r\nContent-Type: text/plain\r\nConnection: Keep-Alive\r\nGoto-Transfer-Encoding: Chunked+Chunked\r\ntransfer-encoding:chunked\r\ntransfer-encoding:chunked\r\n\r\n6\r\nHello \r\n8\r\nGoto 1!\n\r\n", "6\r\nHello \r\n8\r\nGoto 2!\n\r\n", "6\r\nHello \r\n8\r\nGoto 3!\n\r\n0\r\n\r\n"], "responseDelay":"3s", "respondAfterRead": false, "keepOpen": false, "connectionLife": "10s"}'
    ```

    > NOTE: that `goto.goto` FQDN in the above curl script should be replaced with the host fqdn where your goto instance is running (or localhost). 

    > The above curl command can be executed manually, but we can also ask `goto` to configure itself at startup using the above curl command. `Goto` supports startup parameter named `--startupScript` , which allows passing one or more commands to be executed via shell at startup. See the K8S deployment example in the next step for an example.

4. If you're running `Goto` in K8S setup, configure your K8S deployment and service to expose 8081.

    ```yaml
    ---
    apiVersion: v1
    kind: Service
    metadata:
      name: goto
      namespace: goto
      labels:
        goto: goto
    spec:
      selector:
        goto: goto
      ports:
        - port: 8080
          name: http-8080
          targetPort: 8080
          protocol: TCP
        - port: 8081
          name: http-8081
          targetPort: 8081
          protocol: TCP
    ---
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: goto
      namespace: goto
      labels:
        goto: goto
    spec:
      replicas: 1
      selector:
        matchLabels:
          goto: goto
      template:
        metadata:
          labels:
            goto: goto
        spec:
          containers:
            - image: docker.io/uk0000/goto:0.8.15
              args: 
              - --ports
              - 8080,8081/tcp
              - --label
              - goto
              - --startupScript
              - "curl localhost:8080/tcp/8081/configure --data '{\"payload\": true, \"responsePayloads\": [ \"HTTP/1.1 200 OK\\r\\nGoto: goto@${CLUSTER_ID}\\r\\nContent-Type: text/plain\\r\\nConnection: Keep-Alive\\r\\nGoto-Transfer-Encoding: Chunked+Chunked\\r\\ntransfer-encoding:chunked\\r\\ntransfer-encoding:chunked\\r\\n\\r\\n6\\r\\nHello \\r\\n8\\r\\nGoto 1!\\n\\r\\n\", \"6\\r\\nHello \\r\\n8\\r\\nGoto 2!\\n\\r\\n\", \"6\\r\\nHello \\r\\n8\\r\\nGoto 3!\\n\\r\\n0\\r\\n\\r\\n\"], \"responseDelay\":\"3s\", \"respondAfterRead\": false, \"keepOpen\": false, \"connectionLife\": \"10s\"}'"
              name: goto
              ports:
                - containerPort: 8080
                - containerPort: 8081
    ---

    ```

5. If a Service Mesh like `Istio` is in play, you can configure Istio routing so Istio treats `8081` as an HTTP port.
    ```yaml
    ---
    apiVersion: networking.istio.io/v1alpha3
    kind: Gateway
    metadata:
      name: goto-gw
      namespace: goto
    spec:
      selector:
        istio: ingressgateway
      servers:
        - port:
            number: 8080
            name: http
            protocol: HTTP
          hosts:
            - goto.goto
        - port:
            number: 8081
            name: http-8081
            protocol: HTTP
    ---
    apiVersion: networking.istio.io/v1alpha3
    kind: VirtualService
    metadata:
      name: goto-vs
      namespace: goto
    spec:
      gateways:
        - goto/goto-gw
      hosts:
        - goto.goto
      http:
      - match:
        - port: 8080
        route:
        - destination:
            host: goto.goto.svc.cluster.local
            port:
              number: 8080
      - match:
        - port: 8081
        route:
        - destination:
            host: goto.goto.svc.cluster.local
            port:
              number: 8081
    ```


6. Now goto will respond with the given text, which happens to be a bona-fide http protocol response, on port 8081. The below curl request sends an HTTP request and goto sends the HTTP response via TCP port, bypassing golang’s HTTP libraries. Now you can break HTTP protocol whichever way you want, not being limited by language frameworks.

    ```
    curl -v --http1.1 --no-buffer goto.goto:8081
    ```