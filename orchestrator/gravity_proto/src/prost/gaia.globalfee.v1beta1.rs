/// GenesisState - initial state of module
#[derive(Clone, PartialEq, ::prost::Message)]
pub struct GenesisState {
    /// Params of this module
    #[prost(message, optional, tag = "1")]
    pub params: ::core::option::Option<Params>,
}
/// Params defines the set of module parameters.
#[derive(Clone, PartialEq, ::prost::Message)]
pub struct Params {
    /// Minimum stores the minimum gas price(s) for all TX on the chain.
    /// When multiple coins are defined then they are accepted alternatively.
    /// The list must be sorted by denoms asc. No duplicate denoms or zero amount
    /// values allowed. For more information see
    /// <https://docs.cosmos.network/main/modules/auth#concepts>
    #[prost(message, repeated, tag = "1")]
    pub minimum_gas_prices:
        ::prost::alloc::vec::Vec<cosmos_sdk_proto::cosmos::base::v1beta1::DecCoin>,
}
/// QueryMinimumGasPricesRequest is the request type for the
/// Query/MinimumGasPrices RPC method.
#[derive(Clone, PartialEq, ::prost::Message)]
pub struct QueryMinimumGasPricesRequest {}
/// QueryMinimumGasPricesResponse is the response type for the
/// Query/MinimumGasPrices RPC method.
#[derive(Clone, PartialEq, ::prost::Message)]
pub struct QueryMinimumGasPricesResponse {
    #[prost(message, repeated, tag = "1")]
    pub minimum_gas_prices:
        ::prost::alloc::vec::Vec<cosmos_sdk_proto::cosmos::base::v1beta1::DecCoin>,
}
/// Generated client implementations.
pub mod query_client {
    #![allow(unused_variables, dead_code, missing_docs, clippy::let_unit_value)]
    use tonic::codegen::*;
    /// Query defines the gRPC querier service.
    #[derive(Debug, Clone)]
    pub struct QueryClient<T> {
        inner: tonic::client::Grpc<T>,
    }
    impl QueryClient<tonic::transport::Channel> {
        /// Attempt to create a new client by connecting to a given endpoint.
        pub async fn connect<D>(dst: D) -> Result<Self, tonic::transport::Error>
        where
            D: std::convert::TryInto<tonic::transport::Endpoint>,
            D::Error: Into<StdError>,
        {
            let conn = tonic::transport::Endpoint::new(dst)?.connect().await?;
            Ok(Self::new(conn))
        }
    }
    impl<T> QueryClient<T>
    where
        T: tonic::client::GrpcService<tonic::body::BoxBody>,
        T::Error: Into<StdError>,
        T::ResponseBody: Body<Data = Bytes> + Send + 'static,
        <T::ResponseBody as Body>::Error: Into<StdError> + Send,
    {
        pub fn new(inner: T) -> Self {
            let inner = tonic::client::Grpc::new(inner);
            Self { inner }
        }
        pub fn with_interceptor<F>(
            inner: T,
            interceptor: F,
        ) -> QueryClient<InterceptedService<T, F>>
        where
            F: tonic::service::Interceptor,
            T::ResponseBody: Default,
            T: tonic::codegen::Service<
                http::Request<tonic::body::BoxBody>,
                Response = http::Response<
                    <T as tonic::client::GrpcService<tonic::body::BoxBody>>::ResponseBody,
                >,
            >,
            <T as tonic::codegen::Service<http::Request<tonic::body::BoxBody>>>::Error:
                Into<StdError> + Send + Sync,
        {
            QueryClient::new(InterceptedService::new(inner, interceptor))
        }

        pub async fn minimum_gas_prices(
            &mut self,
            request: impl tonic::IntoRequest<super::QueryMinimumGasPricesRequest>,
        ) -> Result<tonic::Response<super::QueryMinimumGasPricesResponse>, tonic::Status> {
            self.inner.ready().await.map_err(|e| {
                tonic::Status::new(
                    tonic::Code::Unknown,
                    format!("Service was not ready: {}", e.into()),
                )
            })?;
            let codec = tonic::codec::ProstCodec::default();
            let path = http::uri::PathAndQuery::from_static(
                "/gaia.globalfee.v1beta1.Query/MinimumGasPrices",
            );
            self.inner.unary(request.into_request(), path, codec).await
        }
    }
}
