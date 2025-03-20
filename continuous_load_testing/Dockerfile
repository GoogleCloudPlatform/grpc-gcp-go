FROM golang
COPY continuous_load_testing /app/bin/continuous_load_testing
# Uncomment the line below to enable CloudPath instead of DirectPath
# ENV DISABLE_DIRECT_PATH=true
CMD /app/bin/continuous_load_testing --methods=EmptyCall