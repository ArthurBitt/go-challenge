FROM eclipse-temurin:21-jre-jammy
COPY elasticmq-server-all-1.6.16.jar /opt/elasticmq.jar
COPY elasticmq.conf /opt/elasticmq.conf
EXPOSE 9324
CMD ["java", "-Dconfig.file=/opt/elasticmq.conf", "-jar", "/opt/elasticmq.jar"]
