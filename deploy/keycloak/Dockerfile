FROM eclipse-temurin:21-jre-jammy
COPY keycloak-24.0.5.tar.gz /tmp/keycloak.tgz
RUN tar -xzf /tmp/keycloak.tgz -C /opt \
    && mv /opt/keycloak-24.0.5 /opt/keycloak \
    && rm /tmp/keycloak.tgz
COPY wager-realm.json /opt/keycloak/data/import/wager-realm.json
ENV KEYCLOAK_ADMIN=admin \
    KEYCLOAK_ADMIN_PASSWORD=admin \
    KC_HTTP_ENABLED=true \
    KC_HOSTNAME_STRICT=false \
    KC_HEALTH_ENABLED=true
EXPOSE 8080
ENTRYPOINT ["/opt/keycloak/bin/kc.sh"]
CMD ["start-dev", "--import-realm"]
